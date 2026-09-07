package mediator

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

type handler func(context.Context, any) (any, error)

// Bus routes commands and queries by their concrete Go type.
type Bus struct {
	mu       sync.RWMutex
	commands map[reflect.Type]handler
	queries  map[reflect.Type]handler
}

// New creates an empty bus.
func New() *Bus {
	return &Bus{commands: make(map[reflect.Type]handler), queries: make(map[reflect.Type]handler)}
}

func messageType(message any) (reflect.Type, error) {
	if message == nil {
		return nil, fmt.Errorf("mediator: nil message")
	}
	return reflect.TypeOf(message), nil
}

func (b *Bus) register(target map[reflect.Type]handler, message any, h handler) error {
	typeOf, err := messageType(message)
	if err != nil || h == nil {
		return fmt.Errorf("mediator: missing handler")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := target[typeOf]; exists {
		return fmt.Errorf("mediator: duplicate handler for %s", typeOf)
	}
	target[typeOf] = h
	return nil
}

// RegisterCommand associates one concrete command type with one handler.
func (b *Bus) RegisterCommand(message any, h func(context.Context, any) (any, error)) error {
	return b.register(b.commands, message, h)
}

// RegisterQuery associates one concrete query type with one handler.
func (b *Bus) RegisterQuery(message any, h func(context.Context, any) (any, error)) error {
	return b.register(b.queries, message, h)
}

func (b *Bus) dispatch(ctx context.Context, target map[reflect.Type]handler, message any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	typeOf, err := messageType(message)
	if err != nil {
		return nil, err
	}
	b.mu.RLock()
	h, exists := target[typeOf]
	b.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("mediator: no handler for %s", typeOf)
	}
	return h(ctx, message)
}

// Send dispatches a command using the caller's original context.
func (b *Bus) Send(ctx context.Context, command any) (any, error) {
	return b.dispatch(ctx, b.commands, command)
}

// Ask dispatches a query using the caller's original context.
func (b *Bus) Ask(ctx context.Context, query any) (any, error) {
	return b.dispatch(ctx, b.queries, query)
}
