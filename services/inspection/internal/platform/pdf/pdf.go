// Package pdf defines the private report-rendering boundary.
package pdf

// AssetBundle must contain embedded or request-authorized derivatives only.
// It never carries storage credentials, bearer tokens, or public URLs.
type AssetBundle map[string][]byte
