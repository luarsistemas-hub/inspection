package harness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIT049ComposeHasIsolatedNotificationSimulators(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	root, err := filepath.Abs("../../../../../")
	if err != nil {
		t.Fatal(err)
	}
	composeFile := filepath.Join(root, "deploy", "docker-compose.yml")
	ports := map[string]string{
		"INSPECTION_MAILPIT_SMTP_PORT": freePort(t),
		"INSPECTION_MAILPIT_UI_PORT":   freePort(t),
		"INSPECTION_TWILIO_FAKE_PORT":  freePort(t),
		"INSPECTION_META_FAKE_PORT":    freePort(t),
	}
	composeRequiredEnv := map[string]string{
		"KEYCLOAK_BOOTSTRAP_ADMIN_USERNAME": "it049-admin",
		"KEYCLOAK_BOOTSTRAP_ADMIN_PASSWORD": "it049-password",
		"INSPECTION_SUPER_ADMIN_USERNAME":   "it049-super-admin",
		"INSPECTION_SUPER_ADMIN_PASSWORD":   "it049-password",
		"INSPECTION_SUPER_ADMIN_SUBJECT":    "6ec9a740-a3dd-5ae5-ae50-9c29d189531b",
	}
	project := fmt.Sprintf("inspection-it049-%d", os.Getpid())

	compose := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", project, "-f", composeFile}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), composeEnv(ports, composeRequiredEnv)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose %v failed: %v\n%s", args, err, output)
		}
	}

	compose("config", "--quiet")
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-p", project, "-f", composeFile, "down", "--volumes", "--remove-orphans")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), composeEnv(ports, composeRequiredEnv)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("docker compose cleanup failed: %v\n%s", err, output)
		}
	})
	compose("up", "-d", "--wait", "--wait-timeout", "60", "mailpit", "twilio-fake", "meta-fake")

	waitForTCP(t, ctx, "127.0.0.1:"+ports["INSPECTION_MAILPIT_SMTP_PORT"])
	waitForHTTP(t, ctx, "http://127.0.0.1:"+ports["INSPECTION_MAILPIT_UI_PORT"]+"/api/v1/info")
	waitForHTTP(t, ctx, "http://127.0.0.1:"+ports["INSPECTION_TWILIO_FAKE_PORT"]+"/__admin/health")
	waitForHTTP(t, ctx, "http://127.0.0.1:"+ports["INSPECTION_META_FAKE_PORT"]+"/__admin/health")
}

func composeEnv(values ...map[string]string) []string {
	overrides := make(map[string]string)
	for _, variables := range values {
		for key, value := range variables {
			overrides[key] = value
		}
	}

	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, overridden := overrides[key]; overridden {
				continue
			}
		}
		env = append(env, item)
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}

func waitForTCP(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	deadline := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			connection.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("TCP endpoint %s did not become ready: %v", address, ctx.Err())
		case <-deadline.C:
		}
	}
}

func waitForHTTP(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		response, err := client.Get(endpoint)
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("HTTP endpoint %s did not become ready: %v", endpoint, ctx.Err())
		case <-deadline.C:
		}
	}
}
