// Package core contains the rebuildable, persistence-neutral dashboard rules.
package core

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Projection struct {
	InspectionID, AssetID, BusinessUnitID, Segment, Status, Classification string
	Flags                                                                  []string
	Invalidated                                                            bool
	UpdatedAt                                                              time.Time
}

type Summary struct{ Critical, Attention, Normal int }

// Reducer accepts a monotonic projection stream. A duplicate is harmless; a
// gap is deferred instead of fabricating a counter that may conceal data.
type Reducer struct {
	Sequence int64
	Rows     map[string]Projection
}

func (r *Reducer) Apply(sequence int64, row Projection) (applied bool, gap bool) {
	if sequence <= r.Sequence {
		return false, false
	}
	if sequence != r.Sequence+1 {
		return false, true
	}
	if r.Rows == nil {
		r.Rows = map[string]Projection{}
	}
	r.Rows[row.InspectionID] = row
	r.Sequence = sequence
	return true, false
}

// Order sorts risk first, then newest state, followed by a stable identifier.
func Order(rows []Projection) {
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := priority(rows[i].Classification), priority(rows[j].Classification)
		if left != right {
			return left < right
		}
		if !rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
		}
		return rows[i].InspectionID < rows[j].InspectionID
	})
}

func Summarize(rows []Projection) Summary {
	var summary Summary
	for _, row := range rows {
		if row.Invalidated {
			continue
		}
		switch row.Classification {
		case "CRITICAL":
			summary.Critical++
		case "ATTENTION":
			summary.Attention++
		case "NORMAL":
			summary.Normal++
		}
	}
	return summary
}

func EncodeCursor(row Projection) string {
	return base64.RawURLEncoding.EncodeToString([]byte(row.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + row.InspectionID))
}
func DecodeCursor(cursor string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	value, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return value, parts[1], nil
}

// EncodeTriageCursor preserves the risk bucket together with the timestamp so
// cursor pagination remains stable when triage is ordered critical-first.
func EncodeTriageCursor(row Projection) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s|%s", priority(row.Classification), row.UpdatedAt.UTC().Format(time.RFC3339Nano), row.InspectionID)))
}

func DecodeTriageCursor(cursor string) (int, time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 3 || parts[2] == "" {
		return 0, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	var bucket int
	if _, err := fmt.Sscanf(parts[0], "%d", &bucket); err != nil || bucket < 0 || bucket > 2 {
		return 0, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	value, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return 0, time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return bucket, value, parts[2], nil
}

func priority(classification string) int {
	switch classification {
	case "CRITICAL":
		return 0
	case "ATTENTION":
		return 1
	default:
		return 2
	}
}
