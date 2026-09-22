// Package llm defines the provider-neutral structured-analysis boundary.
package llm

import (
	"context"
	"time"
)

// StructuredRequest may contain only normalized, already-authorized image data.
// Model is a logical alias; provider/model resolution remains adapter metadata.
type StructuredRequest struct {
	ModelAlias, PromptDigest, Mode string
	SystemPrompt, UserPrompt       string
	JSONSchema                     []byte
	MinimumConfidenceBPS           int
	Images                         []NormalizedImage
}

// NormalizedImage carries explicit lineage so comparative prompts cannot
// silently mix current evidence with the pinned origin snapshot. PairID and
// Position are populated for comparative evidence so the adapter can preserve
// the relationship between an origin image and its current counterpart.
type NormalizedImage struct {
	EvidenceID string
	Source     string
	PairID     string
	Position   string
	DataURL    string
	Digest     string
}

// StructuredResult records actual provider facts, never estimated usage.
type StructuredResult struct {
	JSON                              []byte
	GatewayRequestID, Provider, Model string
	InputTokens, OutputTokens         *int64
	Cost                              *float64
	Latency                           time.Duration
}

type Gateway interface {
	CompleteStructured(context.Context, StructuredRequest) (StructuredResult, error)
}
