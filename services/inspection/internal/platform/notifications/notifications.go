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
