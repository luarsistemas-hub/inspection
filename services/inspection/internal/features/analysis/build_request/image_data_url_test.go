package build_request

import (
	"strings"
	"testing"
)

func TestImageDataURLUsesStoredMimeAndLegacyJPEGDefault(t *testing.T) {
	for _, testCase := range []struct {
		contentType string
		wantPrefix  string
	}{
		{"image/webp", "data:image/webp;base64,"},
		{"image/jpeg", "data:image/jpeg;base64,"},
		{"", "data:image/jpeg;base64,"},
	} {
		value, err := imageDataURL(testCase.contentType, []byte("photo"))
		if err != nil || !strings.HasPrefix(value, testCase.wantPrefix) {
			t.Fatalf("contentType=%q got=%q err=%v", testCase.contentType, value, err)
		}
	}
	if _, err := imageDataURL("image/png", []byte("photo")); err == nil {
		t.Fatal("unsupported image MIME type was accepted")
	}
}
