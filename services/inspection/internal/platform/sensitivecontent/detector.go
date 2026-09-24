package sensitivecontent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"

	_ "github.com/gen2brain/h265/heic"
	_ "golang.org/x/image/webp"
)

const MaxDecodedPixels = 40_000_000

const (
	minFaceComponentPercent = 8
	minFaceAspectRatio      = 0.35
	maxFaceAspectRatio      = 1.50
	maxFaceFrameSpan        = 0.90
	maxFaceFillRatio        = 0.96
	maxFaceBorderContacts   = 1
)

type Region struct {
	Kind                string `json:"kind"`
	X, Y, Width, Height float64
}
type Result struct {
	Regions     []Region
	ModelDigest string
}
type Model interface {
	Detect(context.Context, image.Image) ([]Region, error)
	Bytes() []byte
}
type Detector struct{ Model Model }

type embeddedModel struct{}
type disabledModel struct{}

func NewEmbeddedDetector() Detector { return Detector{Model: embeddedModel{}} }

// NewDisabledDetector creates a detector that clears media without inspecting
// its pixels. It is intended for explicitly configured operational bypasses.
func NewDisabledDetector() Detector { return Detector{Model: disabledModel{}} }

func (embeddedModel) Detect(ctx context.Context, source image.Image) ([]Region, error) {
	bounds := source.Bounds()
	step := max(1, max(bounds.Dx(), bounds.Dy())/128)
	gridWidth := (bounds.Dx() + step - 1) / step
	gridHeight := (bounds.Dy() + step - 1) / step
	skinMask := make([]bool, gridWidth*gridHeight)
	document := newCandidate(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, _ := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(b>>8)
			if skinTone(r8, g8, b8) {
				gridX, gridY := (x-bounds.Min.X)/step, (y-bounds.Min.Y)/step
				skinMask[gridY*gridWidth+gridX] = true
			}
			if max(r8, g8, b8)-min(r8, g8, b8) <= 24 && (r8+g8+b8)/3 >= 210 {
				document.add(x, y)
			}
		}
	}
	total := ((bounds.Dx() + step - 1) / step) * ((bounds.Dy() + step - 1) / step)
	regions := make([]Region, 0, 2)
	if face, ok := faceComponent(skinMask, gridWidth, gridHeight, total); ok {
		regions = append(regions, face.region("FACE", bounds, step))
	}
	if document.count*100 >= total*35 {
		regions = append(regions, document.region("DOCUMENT", bounds, step))
	}
	return regions, nil
}

type skinComponent struct {
	minX, minY, maxX, maxY int
	count                  int
}

func faceComponent(mask []bool, width, height, total int) (skinComponent, bool) {
	visited := make([]bool, len(mask))
	var best skinComponent
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x
			if !mask[index] || visited[index] {
				continue
			}
			component := skinComponent{minX: x, minY: y, maxX: x, maxY: y}
			queue := []int{index}
			visited[index] = true
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				currentX, currentY := current%width, current/width
				component.count++
				component.minX, component.maxX = min(component.minX, currentX), max(component.maxX, currentX)
				component.minY, component.maxY = min(component.minY, currentY), max(component.maxY, currentY)
				for _, neighbor := range []int{current - 1, current + 1, current - width, current + width} {
					if neighbor < 0 || neighbor >= len(mask) || visited[neighbor] || !mask[neighbor] {
						continue
					}
					neighborX, neighborY := neighbor%width, neighbor/width
					if abs(neighborX-currentX)+abs(neighborY-currentY) != 1 {
						continue
					}
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
			if faceLike(component, width, height, total) && component.count > best.count {
				best = component
			}
		}
	}
	return best, best.count > 0
}

func faceLike(component skinComponent, width, height, total int) bool {
	if component.count*100 < total*minFaceComponentPercent {
		return false
	}
	componentWidth := component.maxX - component.minX + 1
	componentHeight := component.maxY - component.minY + 1
	aspectRatio := float64(componentWidth) / float64(componentHeight)
	if aspectRatio < minFaceAspectRatio || aspectRatio > maxFaceAspectRatio {
		return false
	}
	widthRatio := float64(componentWidth) / float64(width)
	heightRatio := float64(componentHeight) / float64(height)
	if widthRatio >= maxFaceFrameSpan && heightRatio >= maxFaceFrameSpan {
		return false
	}

	borderContacts := 0
	if component.minX == 0 {
		borderContacts++
	}
	if component.maxX == width-1 {
		borderContacts++
	}
	if component.minY == 0 {
		borderContacts++
	}
	if component.maxY == height-1 {
		borderContacts++
	}
	if borderContacts > maxFaceBorderContacts {
		return false
	}

	fillRatio := float64(component.count) / float64(componentWidth*componentHeight)
	return fillRatio <= maxFaceFillRatio
}

func (embeddedModel) Bytes() []byte {
	return []byte("inspection-sensitive-region-model-v5:skin-component-r8-aspect35-150-frame90-border1-fill96:document-gray24-luma210")
}

func (disabledModel) Detect(ctx context.Context, _ image.Image) ([]Region, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (disabledModel) Bytes() []byte {
	return []byte("inspection-sensitive-region-model-disabled")
}

type candidate struct {
	minX, minY, maxX, maxY int
	count                  int
}

func newCandidate(bounds image.Rectangle) candidate {
	return candidate{minX: bounds.Max.X, minY: bounds.Max.Y, maxX: bounds.Min.X, maxY: bounds.Min.Y}
}

func (c *candidate) add(x, y int) {
	c.count++
	c.minX, c.minY = min(c.minX, x), min(c.minY, y)
	c.maxX, c.maxY = max(c.maxX, x), max(c.maxY, y)
}

func (c candidate) region(kind string, bounds image.Rectangle, step int) Region {
	return Region{Kind: kind, X: float64(c.minX-bounds.Min.X) / float64(bounds.Dx()), Y: float64(c.minY-bounds.Min.Y) / float64(bounds.Dy()), Width: float64(min(bounds.Max.X, c.maxX+step)-c.minX) / float64(bounds.Dx()), Height: float64(min(bounds.Max.Y, c.maxY+step)-c.minY) / float64(bounds.Dy())}
}

func (c skinComponent) region(kind string, bounds image.Rectangle, step int) Region {
	minX, minY := bounds.Min.X+c.minX*step, bounds.Min.Y+c.minY*step
	maxX, maxY := bounds.Min.X+(c.maxX+1)*step, bounds.Min.Y+(c.maxY+1)*step
	return Region{Kind: kind, X: float64(minX-bounds.Min.X) / float64(bounds.Dx()), Y: float64(minY-bounds.Min.Y) / float64(bounds.Dy()), Width: float64(min(bounds.Max.X, maxX)-minX) / float64(bounds.Dx()), Height: float64(min(bounds.Max.Y, maxY)-minY) / float64(bounds.Dy())}
}

func skinTone(r, g, b int) bool {
	return r > 95 && g > 40 && b > 20 && max(r, g, b)-min(r, g, b) > 15 && abs(r-g) > 15 && r > g && r > b
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (d Detector) Detect(ctx context.Context, data []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if d.Model == nil || len(data) == 0 {
		return Result{}, errors.New("sensitive detector: missing model or image")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > MaxDecodedPixels {
		return Result{}, errors.New("sensitive detector: invalid image")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, errors.New("sensitive detector: invalid image")
	}
	regions, err := d.Model.Detect(ctx, decoded)
	if err != nil {
		return Result{}, err
	}
	for i := range regions {
		if regions[i].Kind != "FACE" && regions[i].Kind != "DOCUMENT" {
			return Result{}, errors.New("sensitive detector: unsupported region")
		}
		regions[i].X = bound(regions[i].X)
		regions[i].Y = bound(regions[i].Y)
		regions[i].Width = bound(regions[i].Width)
		regions[i].Height = bound(regions[i].Height)
	}
	digest := sha256.Sum256(d.Model.Bytes())
	return Result{Regions: regions, ModelDigest: hex.EncodeToString(digest[:])}, nil
}

func bound(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
