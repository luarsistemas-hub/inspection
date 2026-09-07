package sensitivecontent

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

type modelStub struct {
	regions []Region
	err     error
}

func (m modelStub) Detect(context.Context, image.Image) ([]Region, error) { return m.regions, m.err }
func (m modelStub) Bytes() []byte                                         { return []byte("pinned-model-v1") }
func validPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func TestUT009BoundedRegionsAndPinnedDigest(t *testing.T) {
	result, err := (Detector{Model: modelStub{regions: []Region{{Kind: "FACE", X: -1, Y: 0, Width: 2, Height: 1}, {Kind: "DOCUMENT", X: 0, Y: 0, Width: 1, Height: 1}}}}).Detect(context.Background(), validPNG())
	if err != nil || len(result.Regions) != 2 || result.Regions[0].X != 0 || result.Regions[0].Width != 1 || len(result.ModelDigest) != 64 {
		t.Fatalf("bad result: %+v %v", result, err)
	}
}
func TestUT010CorruptAndCanceledImages(t *testing.T) {
	detector := Detector{Model: modelStub{}}
	if _, err := detector.Detect(context.Background(), []byte("bad")); err == nil {
		t.Fatal("corrupt image accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := detector.Detect(ctx, validPNG()); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestEmbeddedModelDetectsFaceAndDocumentSignals(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			switch {
			case x < 40:
				img.Set(x, y, color.RGBA{R: 210, G: 150, B: 110, A: 255})
			default:
				img.Set(x, y, color.RGBA{R: 240, G: 240, B: 240, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	result, err := NewEmbeddedDetector().Detect(context.Background(), encoded.Bytes())
	if err != nil || len(result.Regions) != 2 || result.Regions[0].Kind != "FACE" || result.Regions[1].Kind != "DOCUMENT" {
		t.Fatalf("embedded detection failed: %+v %v", result, err)
	}
}
