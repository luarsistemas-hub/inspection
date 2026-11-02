package accesstest

import (
	"context"
	"errors"
	"sync"

	"inspection/services/inspection/internal/platform/keycloak"
	"inspection/services/inspection/internal/platform/ratelimit"
)

// Provider is an in-memory identity provider that returns one stable subject
// per e-mail, like the real provisioning adapter.
type Provider struct {
	mu       sync.Mutex
	subjects map[string]string
	// Err, when set, is returned by every call (for example a 429 or 5xx).
	Err   error
	Calls int
}

// EnsureInternalUser finds or creates the subject for email.
func (p *Provider) EnsureInternalUser(_ context.Context, email, name string) (keycloak.InternalUser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls++
	if p.Err != nil {
		return keycloak.InternalUser{}, p.Err
	}
	if p.subjects == nil {
		p.subjects = map[string]string{}
	}
	subject, existed := p.subjects[email]
	if !existed {
		subject = "kc-" + email
		p.subjects[email] = subject
	}
	return keycloak.InternalUser{Owner: keycloak.Owner{ID: subject, Email: email, Name: name}, Created: !existed}, nil
}

// Sender records activation links instead of delivering e-mail.
type Sender struct {
	mu   sync.Mutex
	Err  error
	Sent []string
}

// SendInvitation records the destination or fails when Err is set.
func (s *Sender) SendInvitation(_ context.Context, destination, _ string, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	s.Sent = append(s.Sent, destination)
	return nil
}

// ErrProvider is a provider outage used by tests.
var ErrProvider = errors.New("provider unavailable")

// Limits is an in-memory invitation limiter. Deny makes every key refuse.
type Limits struct {
	mu    sync.Mutex
	Deny  bool
	Keys  []string
	Fails error
}

// AllowSend records the key and answers per the configured behavior.
func (l *Limits) AllowSend(_ context.Context, key string) (ratelimit.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Keys = append(l.Keys, key)
	if l.Fails != nil {
		return ratelimit.Result{}, l.Fails
	}
	return ratelimit.Result{Allowed: !l.Deny}, nil
}
