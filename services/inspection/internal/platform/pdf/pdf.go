// Package pdf defines the private report-rendering boundary.
package pdf

import (
	"context"
	"io"
)

// AssetBundle must contain embedded or request-authorized derivatives only.
// It never carries storage credentials, bearer tokens, or public URLs.
type AssetBundle map[string][]byte

type Renderer interface {
	Render(context.Context, io.Reader, AssetBundle) (io.ReadCloser, error)
}
