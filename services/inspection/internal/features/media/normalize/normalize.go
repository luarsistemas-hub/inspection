package normalize

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"

	_ "github.com/gen2brain/h265/heic"
	_ "golang.org/x/image/webp"
)

const maxDecodedPixels = 40_000_000

type Result struct {
	Bytes         []byte
	Width, Height int
	ContentType   string
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
		newWidth, newHeight := int(float64(width)*scale), int(float64(height)*scale)
		target := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
		for y := 0; y < newHeight; y++ {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			for x := 0; x < newWidth; x++ {
				target.Set(x, y, source.At(bounds.Min.X+x*width/newWidth, bounds.Min.Y+y*height/newHeight))
			}
		}
		source = target
		width, height = newWidth, newHeight
	} else {
		target := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(target, target.Bounds(), source, bounds.Min, draw.Src)
		source = target
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, source, &jpeg.Options{Quality: 85}); err != nil {
		return Result{}, err
	}
	return Result{Bytes: output.Bytes(), Width: width, Height: height, ContentType: "image/jpeg"}, nil
}
