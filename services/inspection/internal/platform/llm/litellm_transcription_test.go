package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscriptionGatewayTranscribesLiteralAudioUT011(t *testing.T) {
	audio := []byte("audio bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			ResponseFormat map[string]any `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "gemini-2.5-flash-lite" || len(request.Messages) != 2 {
			t.Fatalf("unexpected request: %+v", request)
		}
		var content []struct {
			Type       string `json:"type"`
			InputAudio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
		}
		if err := json.Unmarshal(request.Messages[1].Content, &content); err != nil {
			t.Fatal(err)
		}
		if len(content) != 2 || content[1].Type != "input_audio" || content[1].InputAudio.Format != "webm" {
			t.Fatalf("audio content missing: %+v", content)
		}
		decoded, err := base64.StdEncoding.DecodeString(content[1].InputAudio.Data)
		if err != nil || !bytes.Equal(decoded, audio) {
			t.Fatalf("unexpected audio bytes: %q, %v", decoded, err)
		}
		w.Header().Set("x-litellm-response-cost", "0.0002")
		// Keep this response equivalent to deploy/wiremock/litellm/transcription.json;
		// WireMock serializes its jsonBody while this adapter test uses the raw body.
		_, _ = w.Write([]byte(`{"id":"stub-transcription-1","provider":"stub","model":"stub/gemini-audio","choices":[{"message":{"content":"{\"transcript\":\"cinco pratos fundos brancos na cozinha\",\"noSpeech\":false}"}}],"usage":{"prompt_tokens":32,"completion_tokens":12,"prompt_tokens_details":{"audio_tokens":20}},"cost":0.0002}`))
	}))
	defer server.Close()

	profile, err := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	if err != nil {
		t.Fatal(err)
	}
	result, err := (HTTPTranscriptionGateway{BaseURL: server.URL}).Transcribe(context.Background(), TranscriptionRequest{
		Profile: profile, Audio: bytes.NewReader(audio), MIME: "audio/webm;codecs=opus", AudioDuration: 30 * time.Second, PromptDigest: "digest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Transcript != "cinco pratos fundos brancos na cozinha" || result.NoSpeech || result.Usage.ReportedCostDecimal == nil || *result.Usage.ReportedCostDecimal != "0.0002" || result.Usage.InputTokens == nil || *result.Usage.InputTokens != 32 || result.Usage.OutputTokens == nil || *result.Usage.OutputTokens != 12 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestTranscriptionGatewayMalformedNoSpeechJSONUT012(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"transcript\":\"\",\"noSpeech\":true,\"unexpected\":true}"}}]}`))
	}))
	defer server.Close()
	profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	_, err := (HTTPTranscriptionGateway{BaseURL: server.URL}).Transcribe(context.Background(), TranscriptionRequest{
		Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/webm", AudioDuration: time.Second, PromptDigest: "digest",
	})
	if CodeOf(err) != string(CodeMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestTranscriptionGatewayRejectsUnsupportedMIMEUT028(t *testing.T) {
	profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	_, err := (HTTPTranscriptionGateway{BaseURL: "http://example.invalid"}).Transcribe(context.Background(), TranscriptionRequest{
		Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/xyz", AudioDuration: time.Second, PromptDigest: "digest",
	})
	if CodeOf(err) != string(CodeInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestTranscriptionProfilesAreKnownAndIndependentUT027(t *testing.T) {
	profile, err := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	if err != nil || profile.ModelAlias != "gemini-2.5-flash-lite" || profile.Version != 1 {
		t.Fatalf("profile not resolved: %+v, %v", profile, err)
	}
	defaultProfile, err := ResolveTranscriptionProfile(DefaultTranscriptionProfile)
	if err != nil || defaultProfile.ModelAlias != "gemini-3.5-flash-lite" {
		t.Fatalf("default profile is not configured for the currently available model: %+v, %v", defaultProfile, err)
	}
	if _, err := ResolveTranscriptionProfile("UNKNOWN"); err == nil {
		t.Fatal("unknown profile must fail")
	}
}

func TestTranscriptionGatewayClassifiesDefinitiveProviderResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   ErrorCode
	}{{http.StatusTooManyRequests, CodeRateLimit}, {http.StatusServiceUnavailable, CodeProviderHTTP}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
			defer server.Close()
			profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
			result, err := (HTTPTranscriptionGateway{BaseURL: server.URL}).Transcribe(context.Background(), TranscriptionRequest{Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/webm", AudioDuration: time.Second, PromptDigest: "digest"})
			if CodeOf(err) != string(tc.want) || result.Usage.HTTPStatus != tc.status || !result.Usage.TransportDelivered {
				t.Fatalf("code=%s usage=%+v err=%v", CodeOf(err), result.Usage, err)
			}
		})
	}
}

func TestTranscriptionGatewayMarksPostSendTimeoutAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancelled"}[cancelled], func(t *testing.T) {
			requested := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(requested)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
			done := make(chan error, 1)
			go func() {
				_, err := (HTTPTranscriptionGateway{BaseURL: server.URL}).Transcribe(ctx, TranscriptionRequest{Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/webm", AudioDuration: time.Second, PromptDigest: "digest"})
				done <- err
			}()
			<-requested
			want := CodeTimeout
			if cancelled {
				cancel()
				want = CodeCancelled
			}
			if err := <-done; CodeOf(err) != string(want) {
				t.Fatalf("error=%v code=%s want=%s", err, CodeOf(err), want)
			}
		})
	}
}

func TestTranscriptionGatewayMarksTransportFailureBeforeRequestWrite(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	result, err := (HTTPTranscriptionGateway{BaseURL: baseURL}).Transcribe(context.Background(), TranscriptionRequest{Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/webm", AudioDuration: time.Second, PromptDigest: "digest"})
	if CodeOf(err) != string(CodeTransport) || result.Usage.TransportDelivered {
		t.Fatalf("pre-send failure code=%s usage=%+v err=%v", CodeOf(err), result.Usage, err)
	}
}

func TestTranscriptionGatewayClassifiesResponseBodyTimeoutAsUncertain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	profile, _ := ResolveTranscriptionProfile("GEMINI_2_5_FLASH_LITE")
	client := &http.Client{Timeout: 60 * time.Millisecond}
	result, err := (HTTPTranscriptionGateway{BaseURL: server.URL, Client: client}).Transcribe(context.Background(), TranscriptionRequest{Profile: profile, Audio: strings.NewReader("audio"), MIME: "audio/webm", AudioDuration: time.Second, PromptDigest: "digest"})
	if CodeOf(err) != string(CodeTimeout) || !result.Usage.TransportDelivered {
		t.Fatalf("body timeout code=%s usage=%+v err=%v", CodeOf(err), result.Usage, err)
	}
}
