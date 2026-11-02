package llm

import (
	"context"
	"io"
	"time"
)

// DefaultTranscriptionTimeout is the maximum HTTP transport duration used by the gateway.
const DefaultTranscriptionTimeout = 45 * time.Second

// TranscriptionProfile is an independently versioned audio model alias.
type TranscriptionProfile struct {
	Key             string
	Version         int
	ModelAlias      string
	PromptVersion   string
	MaxOutputTokens int
}

// TranscriptionProfiles contains audio-only aliases, separate from vision aliases.
var TranscriptionProfiles = map[string]TranscriptionProfile{
	// Keep the persisted profile readable for jobs already admitted by the current worktree.
	"GEMINI_2_5_FLASH_LITE": {Key: "GEMINI_2_5_FLASH_LITE", Version: 1, ModelAlias: "gemini-2.5-flash-lite", PromptVersion: "literal-v1", MaxOutputTokens: 512},
	"GEMINI_3_5_FLASH_LITE": {Key: "GEMINI_3_5_FLASH_LITE", Version: 1, ModelAlias: "gemini-3.5-flash-lite", PromptVersion: "literal-v1", MaxOutputTokens: 512},
	"GEMINI_2_0_FLASH_LITE": {Key: "GEMINI_2_0_FLASH_LITE", Version: 1, ModelAlias: "gemini-2.0-flash-lite", PromptVersion: "literal-v1", MaxOutputTokens: 512},
}

// DefaultTranscriptionProfile is the independently selected audio profile.
const DefaultTranscriptionProfile = "GEMINI_2_5_FLASH_LITE"

// ResolveTranscriptionProfile validates the dedicated worker's configured alias.
func ResolveTranscriptionProfile(key string) (TranscriptionProfile, error) {
	profile, ok := TranscriptionProfiles[key]
	if !ok {
		return TranscriptionProfile{}, NewError(CodeInvalidInput, 0, errUnknownTranscriptionProfile)
	}
	return profile, nil
}

// TranscriptionRequest contains a bounded audio payload and its capture metadata.
type TranscriptionRequest struct {
	Profile       TranscriptionProfile
	Audio         io.Reader
	MIME          string
	AudioDuration time.Duration
	PromptDigest  string
}

// TranscriptionResult is a literal transcript or an explicit no-speech result.
type TranscriptionResult struct {
	Transcript string
	NoSpeech   bool
	Usage      StructuredResult
}

// TranscriptionGateway accepts audio only and is independent of the vision gateway.
type TranscriptionGateway interface {
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResult, error)
}
