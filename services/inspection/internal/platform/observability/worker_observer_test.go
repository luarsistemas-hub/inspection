package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWorkerObserverLogsDLQReasonWithoutRawErrorDetails(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	observer := NewWorkerObserver(nil, nil)
	observer.AfterDelivery(context.Background(), DeliveryObservation{
		Queue:     "inspection.video",
		EventType: "capture.video_submitted.v2",
		Action:    "dlq",
		Result:    "published",
		Reason:    "permanent_rejection",
		Attempt:   0,
		Duration:  time.Millisecond,
		Err:       errors.New("database detail tenant=secret-customer-id"),
	})

	log := output.String()
	for _, expected := range []string{"messaging delivery sent to dead letter queue", `"queue":"inspection.video"`, `"eventType":"capture.video_submitted.v2"`, `"reason":"permanent_rejection"`, `"errorCode":"internal_error"`} {
		if !strings.Contains(log, expected) {
			t.Errorf("DLQ log missing %q: %s", expected, log)
		}
	}
	if strings.Contains(log, "secret-customer-id") || strings.Contains(log, "database detail") {
		t.Fatalf("DLQ log leaked raw error details: %s", log)
	}
}
