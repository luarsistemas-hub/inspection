//go:build integration

package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

func TestMediaUploadOutboxRabbitInboxDeduplicatesIT556(t *testing.T) {
	rabbitURL := os.Getenv("INSPECTION_TEST_RABBITMQ_URL")
	if rabbitURL == "" {
		t.Skip("INSPECTION_TEST_RABBITMQ_URL is required")
	}
	connection, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	publishChannel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer publishChannel.Close()
	consumeChannel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer consumeChannel.Close()
	queueName := "it556-" + identity.NewID().String()
	contract, err := NewQueueContract(queueName, "media.upload_completed.v1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeclareTopology(publishChannel, []QueueContract{contract}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range append([]string{contract.Name, contract.DLQName}, contract.RetryNames[:]...) {
			_, _ = publishChannel.QueueDelete(name, false, false, false)
		}
	})

	db := sqliteDB(t)
	mediaID, responsibilityID, tenantID := identity.NewID(), identity.NewID(), identity.NewID()
	envelope := events.Envelope[map[string]any]{
		ID: identity.NewID(), Type: "media.upload_completed.v1", SchemaVersion: 1,
		OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: mediaID,
		CorrelationID: "it556-" + mediaID.String(),
		Payload:       map[string]any{"mediaId": mediaID, "responsibilityId": responsibilityID, "status": "RECEIVED_PENDING_VALIDATION"},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddOutbox(db, envelope); err != nil {
		t.Fatalf("insert upload-completed outbox event: %v", err)
	}
	consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: queueName, Handle: func(_ context.Context, tx *gorm.DB, raw events.RawEnvelope) error {
		return tx.Create(&outcome{EventID: raw.ID.String()}).Error
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (RabbitConsumer{Channel: consumeChannel, Contract: contract, Handler: consumer, ConsumerName: queueName}).Run(ctx)
	}()
	publisher, err := NewRabbitPublisher(publishChannel)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := Dispatcher{DB: db, Publisher: publisher}
	if count, err := dispatcher.Dispatch(ctx); err != nil || count != 1 {
		t.Fatalf("outbox dispatch count=%d err=%v", count, err)
	}
	if err := publisher.PublishConfirmed(ctx, Publication{RoutingKey: envelope.Type, Body: body, Mandatory: true, CorrelationID: envelope.CorrelationID}); err != nil {
		t.Fatalf("publish duplicate delivery: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := publishChannel.QueueInspect(contract.Name)
		if err == nil && stats.Messages == 0 {
			var outcomes, inbox int64
			if err := db.Model(&outcome{}).Count(&outcomes).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&database.InboxReceipt{}).Where("consumer=? AND event_id=?", queueName, envelope.ID).Count(&inbox).Error; err != nil {
				t.Fatal(err)
			}
			if outcomes != 1 || inbox != 1 {
				t.Fatalf("duplicate upload event changed one logical result: outcomes=%d inbox=%d", outcomes, inbox)
			}
			cancel()
			if runErr := <-done; runErr != nil && runErr != context.Canceled {
				t.Fatal(runErr)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	select {
	case runErr := <-done:
		t.Fatalf("timed out waiting for Rabbit inbox completion (consumer=%v)", runErr)
	case <-time.After(100 * time.Millisecond):
		t.Fatal(fmt.Errorf("timed out waiting for Rabbit inbox completion"))
	}
}
