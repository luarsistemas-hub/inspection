package normalize

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"testing"

	"github.com/gen2brain/h265/heic"
)

func TestSupportedWebPAndHEICNormalizeToJPEG(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	assertNormalized(t, webp)

	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := heic.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	assertNormalized(t, encoded.Bytes())
}

func assertNormalized(t *testing.T, data []byte) {
	t.Helper()
	result, err := Image(context.Background(), data, 2048)
	if err != nil || result.ContentType != "image/jpeg" || len(result.Bytes) == 0 {
		t.Fatalf("normalization failed: %+v %v", result, err)
	}
}
