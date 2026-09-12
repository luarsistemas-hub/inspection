// Package llm defines the provider-neutral structured-analysis boundary.
package llm

import (
	"context"
	"time"
)

// StructuredRequest may contain only normalized, already-authorized image data.
// Model is a logical alias; provider/model resolution remains adapter metadata.
type StructuredRequest struct {
	ModelAlias, PromptVersion string
	JSONSchema                []byte
	Images                    []NormalizedImage
}

// NormalizedImage carries explicit lineage so comparative prompts cannot
// silently mix current evidence with the pinned origin snapshot.
type NormalizedImage struct{ EvidenceID, Source, DataURL, Digest string }

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
