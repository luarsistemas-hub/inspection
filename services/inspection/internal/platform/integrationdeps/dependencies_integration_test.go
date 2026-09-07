//go:build integration

package integrationdeps

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/ratelimit"

	"github.com/minio/minio-go/v7"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const startupTimeout = 90 * time.Second

func TestTask02DependencyHarnesses(t *testing.T) {
	t.Run("RabbitMQ", testRabbitMQ)
	t.Run("MinIO", testMinIO)
	t.Run("Dragonfly", testDragonfly)
	t.Run("Mailpit", testMailpit)
	t.Run("TwilioFake", testTwilioFake)
}

func testRabbitMQ(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, "rabbitmq:4.1.3-management-alpine",
		testcontainers.WithEnv(map[string]string{"RABBITMQ_DEFAULT_USER": "inspection", "RABBITMQ_DEFAULT_PASS": "inspection"}),
		testcontainers.WithExposedPorts("5672/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5672/tcp").WithStartupTimeout(startupTimeout)))
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := container.Endpoint(ctx, "amqp")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := amqp.Dial("amqp://inspection:inspection@" + strings.TrimPrefix(endpoint, "amqp://") + "/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	contract, _ := messaging.NewQueueContract("task02.events", "participant.channel_verified.v1", 8)
	if err := messaging.DeclareTopology(channel, []messaging.QueueContract{contract}); err != nil {
		t.Fatal(err)
	}
	if queue, err := channel.QueueInspect(contract.DLQName); err != nil || queue.Name != contract.DLQName {
		t.Fatalf("dlq=%+v err=%v", queue, err)
	}
}

func testMinIO(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, "minio/minio:RELEASE.2025-04-22T22-12-26Z",
		testcontainers.WithEnv(map[string]string{"MINIO_ROOT_USER": "inspection", "MINIO_ROOT_PASSWORD": "inspection-secret"}),
		testcontainers.WithCmd("server", "/data"),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(startupTimeout)))
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := objectstore.NewMinIO(endpoint, "inspection", "inspection-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Client.MakeBucket(ctx, "inspection-private", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + endpoint + "/inspection-private/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous status=%d", response.StatusCode)
	}
}

func testDragonfly(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, "docker.dragonflydb.io/dragonflydb/dragonfly:v1.34.2",
		testcontainers.WithCmd("dragonfly", "--proactor_threads=2", "--maxmemory=512mb"),
		testcontainers.WithExposedPorts("6379/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("6379/tcp").WithStartupTimeout(startupTimeout)))
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	store := ratelimit.DragonflyStore{Address: endpoint}
	first, err := store.Take(ctx, "task02", 1, time.Minute, time.Now())
	if err != nil || !first.Allowed {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.Take(ctx, "task02", 1, time.Minute, time.Now())
	if err != nil || second.Allowed {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func testMailpit(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, "axllent/mailpit:v1.27.8",
		testcontainers.WithExposedPorts("1025/tcp", "8025/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/api/v1/info").WithPort("8025/tcp").WithStartupTimeout(startupTimeout)))
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatal(err)
	}
	sender := notifications.SMTPSender{Address: "localhost:" + port.Port(), From: "inspection@example.test"}
	if _, err := sender.Send(ctx, notifications.Intent{ID: "mailpit", Destination: "person@example.test", Template: "OTP", Parameters: map[string]string{"body": "code"}}); err != nil {
		t.Fatal(err)
	}
}

func testTwilioFake(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, "wiremock/wiremock:3.13.1",
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/__admin/mappings").WithPort("8080/tcp").WithStartupTimeout(startupTimeout)))
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := container.Endpoint(ctx, "http")
	if err != nil {
		t.Fatal(err)
	}
	mapping := []byte(`{"request":{"method":"POST","urlPathPattern":"/2010-04-01/Accounts/.*/Messages.json"},"response":{"status":201,"headers":{"X-Message-Sid":"SM-test"},"jsonBody":{"sid":"SM-test"}}}`)
	response, err := http.Post(endpoint+"/__admin/mappings", "application/json", bytes.NewReader(mapping))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		t.Fatalf("mapping status=%d", response.StatusCode)
	}
	sender := notifications.TwilioSender{BaseURL: endpoint, AccountSID: "AC-test", AuthToken: "token", From: "+15550000000", Channel: notifications.SMS}
	receipt, err := sender.Send(ctx, notifications.Intent{ID: "twilio", Destination: "+15550000001", Parameters: map[string]string{"body": "code"}})
	if err != nil || receipt.ID != "SM-test" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
