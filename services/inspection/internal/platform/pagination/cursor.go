// Package pagination contains the cursor contract shared by tenant-owned collections.
package pagination

import (
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	graph "inspection/services/inspection/internal/platform/graphql"
)

// After decodes the opaque cursor used by collection queries.
func After(value string) (time.Time, identity.ID, error) {
	cursor, err := graph.DecodeCursor(value)
	if err != nil {
		return time.Time{}, identity.ID{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, cursor.Time)
	if err != nil {
		return time.Time{}, identity.ID{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	id, err := identity.ParseID(cursor.ID)
	if err != nil {
		return time.Time{}, identity.ID{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	return at, id, nil
}

// Encode returns a cursor ordered by creation instant and UUID tie-breaker.
func Encode(createdAt time.Time, id identity.ID) string {
	return graph.EncodeCursor(graph.Cursor{Time: createdAt.UTC().Format(time.RFC3339Nano), ID: id.String()})
}
