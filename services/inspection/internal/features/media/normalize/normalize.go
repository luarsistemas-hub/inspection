package normalize

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"math"

	_ "github.com/gen2brain/h265/heic"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxDecodedPixels = 40_000_000

type Result struct {
	Bytes         []byte
	Width, Height int
	ContentType   string
	Profile       string
}

func Image(ctx context.Context, data []byte, maxDimension int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if maxDimension <= 0 {
		return Result{}, errors.New("normalize: invalid dimension")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxDecodedPixels {
		return Result{}, errors.New("normalize: invalid image")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, errors.New("normalize: invalid image")
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > maxDimension || height > maxDimension {
		scale := float64(maxDimension) / float64(width)
		if height > width {
			scale = float64(maxDimension) / float64(height)
		}
		newWidth, newHeight := max(1, int(math.Round(float64(width)*scale))), max(1, int(math.Round(float64(height)*scale)))
		target := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		xdraw.CatmullRom.Scale(target, target.Bounds(), source, bounds, xdraw.Src, nil)
		source = target
		width, height = newWidth, newHeight
	} else {
		target := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.Draw(target, target.Bounds(), source, bounds.Min, xdraw.Src)
		source = target
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, source, &jpeg.Options{Quality: 85}); err != nil {
		return Result{}, err
	}
	return Result{Bytes: output.Bytes(), Width: width, Height: height, ContentType: "image/jpeg", Profile: "normalized-jpeg-q85-v2"}, nil
}

// Analysis keeps already-normalized JPEG/WebP bytes when they are small enough
// and contain no image metadata. Other inputs use the canonical JPEG path.
func Analysis(ctx context.Context, data []byte, maxDimension int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if maxDimension <= 0 {
		return Result{}, errors.New("normalize: invalid dimension")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxDecodedPixels {
		return Result{}, errors.New("normalize: invalid image")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return Result{}, errors.New("normalize: invalid image")
	}
	if config.Width <= maxDimension && config.Height <= maxDimension {
		metadataFree := (format == "jpeg" && jpegHasNoMetadata(data)) || (format == "webp" && webpHasNoMetadata(data))
		if metadataFree {
			mime := "image/jpeg"
			if format == "webp" {
				mime = "image/webp"
			}
			return Result{Bytes: append([]byte(nil), data...), Width: config.Width, Height: config.Height, ContentType: mime, Profile: "analysis-pass-through-v1"}, nil
		}
	}
	return Image(ctx, data, maxDimension)
}

func jpegHasNoMetadata(data []byte) bool {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return false
	}
	for offset := 2; offset < len(data); {
		if data[offset] != 0xff {
			return false
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return false
		}
		marker := data[offset]
		offset++
		if marker == 0xda { // Scan data follows; metadata segments can no longer occur.
			return true
		}
		if marker == 0xd8 || marker == 0xd9 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if offset+2 > len(data) {
			return false
		}
		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(data) {
			return false
		}
		end := offset + segmentLength
		if marker == 0xe0 {
			payload := data[offset+2 : end]
			if !bytes.HasPrefix(payload, []byte("JFIF\x00")) && !bytes.HasPrefix(payload, []byte("JFXX\x00")) {
				return false
			}
		} else if marker >= 0xe1 && marker <= 0xef || marker == 0xfe {
			return false
		}
		offset = end
	}
	return false
}

func webpHasNoMetadata(data []byte) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset+8 <= len(data); {
		kind := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if size < 0 || offset+8+size > len(data) {
			return false
		}
		if kind == "EXIF" || kind == "XMP " || kind == "ICCP" {
			return false
		}
		offset += 8 + size + (size & 1)
	}
	return true
}
