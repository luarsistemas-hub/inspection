package pdf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGotenbergRejectsExternalURLsAndReturnsPDF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forms/chromium/convert/html" {
			t.Fatal(r.URL.Path)
		}
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer server.Close()
	if _, err := (Gotenberg{BaseURL: server.URL}).Render(context.Background(), strings.NewReader(`<img src="https://unsafe">`), nil); err == nil {
		t.Fatal("external URL allowed")
	}
	pdf, err := (Gotenberg{BaseURL: server.URL}).Render(context.Background(), strings.NewReader(`<h1>private</h1>`), AssetBundle{"image.png": []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	defer pdf.Close()
	body, _ := io.ReadAll(pdf)
	if string(body) != "%PDF-1.7" {
		t.Fatal("renderer bytes lost")
	}
}

func TestGotenbergRejectsEmptyOrNonPDFResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not a pdf")) }))
	defer server.Close()
	if _, err := (Gotenberg{BaseURL: server.URL}).Render(context.Background(), strings.NewReader(`<h1>private</h1>`), nil); err == nil {
		t.Fatal("non-PDF response accepted")
	}
}
