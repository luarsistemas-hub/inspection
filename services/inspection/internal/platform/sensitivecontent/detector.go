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

func NewEmbeddedDetector() Detector { return Detector{Model: embeddedModel{}} }

func (embeddedModel) Detect(ctx context.Context, source image.Image) ([]Region, error) {
	bounds := source.Bounds()
	step := max(1, max(bounds.Dx(), bounds.Dy())/128)
	skin := newCandidate(bounds)
	document := newCandidate(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, _ := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(b>>8)
			if skinTone(r8, g8, b8) {
				skin.add(x, y)
			}
			if max(r8, g8, b8)-min(r8, g8, b8) <= 24 && (r8+g8+b8)/3 >= 210 {
				document.add(x, y)
			}
		}
	}
	total := ((bounds.Dx() + step - 1) / step) * ((bounds.Dy() + step - 1) / step)
	regions := make([]Region, 0, 2)
	if skin.count*100 >= total*2 {
		regions = append(regions, skin.region("FACE", bounds, step))
	}
	if document.count*100 >= total*35 {
		regions = append(regions, document.region("DOCUMENT", bounds, step))
	}
	return regions, nil
}

func (embeddedModel) Bytes() []byte {
	return []byte("inspection-sensitive-region-model-v2:skin-r95-g40-b20:document-gray24-luma210")
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
