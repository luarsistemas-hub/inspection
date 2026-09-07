package feature

import (
	"fmt"
	"net/http"
	"sync"
)

// Registry collects operation entry points while keeping adapters out of slices.
type Registry struct {
	mu      sync.Mutex
	routes  map[string]http.Handler
	entries map[string]string
}

// NewRegistry creates an empty startup registry.
func NewRegistry() *Registry {
	return &Registry{routes: make(map[string]http.Handler), entries: make(map[string]string)}
}

// RegisterHTTP registers exactly one method/path entry for a slice.
func (r *Registry) RegisterHTTP(slice, method, path string, handler http.Handler) error {
	if slice == "" || method == "" || path == "" || handler == nil {
		return fmt.Errorf("slice %q: missing HTTP dependency", slice)
	}
	key := method + " " + path
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner, ok := r.entries[key]; ok {
		return fmt.Errorf("slice %q: entry %s already registered by %s", slice, key, owner)
	}
	r.entries[key], r.routes[key] = slice, handler
	return nil
}

// Mount copies registered routes to a standard ServeMux.
func (r *Registry) Mount(mux *http.ServeMux) error {
	if mux == nil {
		return fmt.Errorf("feature registry: missing router")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, handler := range r.routes {
		mux.Handle(key, handler)
	}
	return nil
}

// Count reports registered primary entry points.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}
