package feature

import (
	"net/http"
	"testing"
)

func TestRegistryContractsUT001UT002(t *testing.T) {
	r := NewRegistry()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if err := r.RegisterHTTP("slice/a", "GET", "/a", h); err != nil {
		t.Fatal(err)
	}
	if r.Count() != 1 {
		t.Fatalf("count=%d", r.Count())
	}
	if err := r.RegisterHTTP("slice/b", "GET", "/a", h); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := r.RegisterHTTP("slice/c", "GET", "/c", nil); err == nil {
		t.Fatal("expected dependency error")
	}
	if err := r.Mount(nil); err == nil {
		t.Fatal("expected router error")
	}
}
