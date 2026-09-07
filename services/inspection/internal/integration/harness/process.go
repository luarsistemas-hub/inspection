package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ManagedProcess is an opt-in local-process lifecycle used by the external
// suite when API, worker, or scheduler binaries are not already running.
// Context cancellation terminates the child process.
type ManagedProcess struct {
	Name string
	cmd  *exec.Cmd
	mu   sync.Mutex
	out  processOutput
	done chan struct{}
	err  error
}

type processOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *processOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *processOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

// StartProcess starts one service command with an isolated environment overlay.
// The harness never starts processes implicitly; callers choose the exact
// binary and arguments so CI can use prebuilt artifacts or `go run`.
func (h *Harness) StartProcess(ctx context.Context, name, dir, executable string, args []string, environment map[string]string) (*ManagedProcess, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(executable) == "" {
		return nil, errors.New("integration harness: process name and executable are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for key, value := range environment {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	managed := &ManagedProcess{Name: name, cmd: cmd, done: make(chan struct{})}
	cmd.Stdout = &managed.out
	cmd.Stderr = &managed.out
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	go func() {
		managed.mu.Lock()
		managed.err = cmd.Wait()
		close(managed.done)
		managed.mu.Unlock()
	}()
	return managed, nil
}

// WaitReady polls a service HTTP endpoint until it responds successfully or
// the process/context stops first.
func (p *ManagedProcess) WaitReady(ctx context.Context, client *http.Client, endpoint string) error {
	if p == nil || p.cmd == nil {
		return errors.New("integration harness: process unavailable")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("integration harness: readiness endpoint is required")
	}
	interval := 100 * time.Millisecond
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-p.done:
			if err := p.Wait(); err != nil {
				return fmt.Errorf("process %s exited while waiting for readiness: %w\n%s", p.Name, err, p.Output())
			}
			return fmt.Errorf("process %s exited while waiting for readiness\n%s", p.Name, p.Output())
		default:
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Output returns the captured process stdout/stderr.
func (p *ManagedProcess) Output() string {
	if p == nil {
		return ""
	}
	return p.out.String()
}

// Wait returns the process exit result.
func (p *ManagedProcess) Wait() error {
	if p == nil {
		return errors.New("integration harness: process unavailable")
	}
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Close terminates the process and waits for it to exit.
func (p *ManagedProcess) Close() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && !strings.Contains(err.Error(), "already finished") {
		return err
	}
	<-p.done
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	if err != nil && !strings.Contains(err.Error(), "signal: killed") {
		return err
	}
	return nil
}
