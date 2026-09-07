package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// Serve runs an HTTP composition root with bounded graceful shutdown.
func Serve(address string, handler http.Handler, timeout time.Duration) error {
	return ServeWithBackground(address, handler, timeout, nil)
}

func ServeWithBackground(address string, handler http.Handler, timeout time.Duration, background func(context.Context) error) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	if background != nil {
		go func() { errCh <- background(ctx) }()
	}
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		if err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
