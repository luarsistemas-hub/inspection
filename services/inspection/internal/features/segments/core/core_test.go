package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSegmentSchemaAndAttributesIT041IT043IT051ToIT053(t *testing.T) {
	payload := []byte(`{"type":"object","properties":{"address":{"type":"string","maxLength":2000},"units":{"type":"integer"}},"required":["address"],"additionalProperties":false}`)
	canonical, digest, schema, err := ValidateSchema(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) == 0 || len(digest) != 64 {
		t.Fatal("schema was not canonicalized")
	}
	if err := ValidateAttributes(schema, map[string]any{"address": "A", "units": float64(2)}); err != nil {
		t.Fatal(err)
	}
	for _, attrs := range []map[string]any{{}, {"unknown": "x"}, {"address": 1}, {"address": strings.Repeat("x", 2001)}} {
		if ValidateAttributes(schema, attrs) == nil {
			t.Fatalf("invalid attributes accepted: %#v", attrs)
		}
	}
	var decoded map[string]any
	if json.Unmarshal(canonical, &decoded) != nil {
		t.Fatal("canonical schema is not JSON")
	}
}

func TestSegmentSchemaRejectsUnknownAndLimitsIT041IT043(t *testing.T) {
	for _, payload := range [][]byte{[]byte(`{"type":"array","properties":{"x":{"type":"string"}}}`), []byte(`{"type":"object","properties":{"x":{"type":"object"}}}`), []byte(`{"type":"object","properties":{"x":{"type":"string"}},"required":["y"]}`)} {
		if _, _, _, err := ValidateSchema(payload); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
