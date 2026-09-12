package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Channel string

const (
	Email    Channel = "EMAIL"
	WhatsApp Channel = "WHATSAPP"
	SMS      Channel = "SMS"
)

var ErrUnknownChannel = errors.New("unknown notification channel")

// Provider identifies an externally configured notification transport. It is
// persisted with operational work before an adapter is invoked.
type Provider string

const (
	ProviderSMTP   Provider = "smtp"
	ProviderTwilio Provider = "twilio"
	ProviderMeta   Provider = "meta"
)

// DeliveryIntent is the provider-facing, rendered form of an operational
// notification. Business slices never construct it or see provider settings.
type DeliveryIntent struct {
	ID, Destination, Template, Language string
	Subject, Text, HTML                 string
	TemplateParameters                  []string
	CallbackURL, TenantID               string
}

// Adapter is one concrete channel/provider implementation.
type Adapter interface {
	Send(context.Context, DeliveryIntent) (Receipt, error)
}

type adapterKey struct {
	channel  Channel
	provider Provider
}

// Gateway routes only to a provider that was selected before delivery. It
// deliberately contains no provider or channel fallback behavior.
type Gateway struct{ adapters map[adapterKey]Adapter }

// NewGateway validates and copies the channel/provider adapter registry.
func NewGateway(adapters map[Channel]map[Provider]Adapter) (*Gateway, error) {
	result := &Gateway{adapters: make(map[adapterKey]Adapter)}
	for channel, providers := range adapters {
		if channel != Email && channel != SMS && channel != WhatsApp {
			return nil, ErrUnknownChannel
		}
		for provider, adapter := range providers {
			if adapter == nil || provider == "" {
				return nil, errors.New("notification gateway: invalid adapter")
			}
			result.adapters[adapterKey{channel: channel, provider: provider}] = adapter
		}
	}
	return result, nil
}

// Send invokes exactly the persisted provider for the channel.
func (g *Gateway) Send(ctx context.Context, channel Channel, provider Provider, intent DeliveryIntent) (Receipt, error) {
	if g == nil {
		return Receipt{}, errors.New("notification gateway: unavailable")
	}
	adapter, ok := g.adapters[adapterKey{channel: channel, provider: provider}]
	if !ok {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "provider_not_configured", PreSend: true}
	}
	return adapter.Send(ctx, intent)
}

// ProviderResolver chooses the one configured provider for a channel when a
// durable notification request is first persisted.
type ProviderResolver struct{ providers map[Channel]Provider }

// NewProviderResolver creates the immutable operational provider selection.
func NewProviderResolver(whatsApp Provider) (*ProviderResolver, error) {
	if whatsApp != ProviderTwilio && whatsApp != ProviderMeta {
		return nil, errors.New("notification gateway: invalid WhatsApp provider")
	}
	return &ProviderResolver{providers: map[Channel]Provider{Email: ProviderSMTP, SMS: ProviderTwilio, WhatsApp: whatsApp}}, nil
}

// ProviderFor returns the provider that must be persisted for the channel.
func (r *ProviderResolver) ProviderFor(channel Channel) (Provider, error) {
	if r == nil {
		return "", errors.New("notification gateway: missing provider resolver")
	}
	provider, ok := r.providers[channel]
	if !ok {
		return "", ErrUnknownChannel
	}
	return provider, nil
}

// ErrorKind classifies retry safety without exposing remote response content.
type ErrorKind string

const (
	ErrorPermanent ErrorKind = "permanent"
	ErrorTransient ErrorKind = "transient"
	ErrorUnknown   ErrorKind = "unknown"
	ErrorCanceled  ErrorKind = "canceled"
)

// DeliveryError is safe to persist and expose to the worker state machine.
type DeliveryError struct {
	Kind       ErrorKind
	Code       string
	RetryAfter time.Duration
	PreSend    bool
}

func (e *DeliveryError) Error() string {
	if e == nil || e.Code == "" {
		return "notification delivery failed"
	}
	return "notification delivery failed: " + e.Code
}

// AsDeliveryError extracts normalized adapter failures.
func AsDeliveryError(err error) (*DeliveryError, bool) {
	var deliveryError *DeliveryError
	return deliveryError, errors.As(err, &deliveryError)
}

type Intent struct {
	ID, Destination, Template string
	Parameters                map[string]string
}
type Receipt struct {
	Provider, ID string
	AcceptedAt   time.Time
}
type Sender interface {
	Send(context.Context, Intent) (Receipt, error)
}
type Registry struct{ senders map[Channel]Sender }

func NewRegistry(senders map[Channel]Sender) (*Registry, error) {
	copyMap := make(map[Channel]Sender, len(senders))
	for channel, sender := range senders {
		if sender == nil || (channel != Email && channel != WhatsApp && channel != SMS) {
			return nil, ErrUnknownChannel
		}
		copyMap[channel] = sender
	}
	return &Registry{senders: copyMap}, nil
}
func (r *Registry) Sender(channel Channel) (Sender, error) {
	sender, ok := r.senders[channel]
	if !ok {
		return nil, ErrUnknownChannel
	}
	return sender, nil
}

type Attempt struct {
	Channel Channel
	Receipt *Receipt
	Err     error
}
type Aggregate struct {
	Status   string
	Attempts []Attempt
}

func Deliver(ctx context.Context, registry *Registry, intents map[Channel]Intent) Aggregate {
	result := Aggregate{Status: "FAILED", Attempts: make([]Attempt, 0, len(intents))}
	if registry == nil || len(intents) == 0 {
		return result
	}
	type item struct {
		channel Channel
		receipt Receipt
		err     error
	}
	ch := make(chan item, len(intents))
	var wg sync.WaitGroup
	for channel, intent := range intents {
		wg.Add(1)
		go func(channel Channel, intent Intent) {
			defer wg.Done()
			sender, err := registry.Sender(channel)
			if err != nil {
				ch <- item{channel: channel, err: err}
				return
			}
			receipt, err := sender.Send(ctx, intent)
			ch <- item{channel: channel, receipt: receipt, err: err}
		}(channel, intent)
	}
	wg.Wait()
	close(ch)
	for value := range ch {
		attempt := Attempt{Channel: value.channel, Err: value.err}
		if value.err == nil {
			attempt.Receipt = &value.receipt
			result.Status = "DELIVERED"
		}
		result.Attempts = append(result.Attempts, attempt)
	}
	return result
}

func VerifyTwilioSignature(authToken, rawURL string, form map[string][]string, signature string) bool {
	if authToken == "" || rawURL == "" || signature == "" {
		return false
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var b strings.Builder
	b.WriteString(rawURL)
	for _, key := range keys {
		values := form[key]
		for _, value := range values {
			b.WriteString(key)
			b.WriteString(value)
		}
	}
	mac := hmac.New(sha1.New, []byte(authToken))
	_, _ = mac.Write([]byte(b.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func ValidateCallbackTimestamp(raw string, now time.Time, tolerance time.Duration) error {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return errors.New("invalid callback timestamp")
	}
	delta := now.Sub(time.Unix(seconds, 0))
	if delta < 0 {
		delta = -delta
	}
	if delta > tolerance {
		return fmt.Errorf("stale callback")
	}
	return nil
}
