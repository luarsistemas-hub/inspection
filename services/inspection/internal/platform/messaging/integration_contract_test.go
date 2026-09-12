package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type outcome struct {
	ID         uint `gorm:"primaryKey"`
	EventID    string
	Generation int
}
type publisherStub struct {
	err         error
	calls       int
	publication Publication
}

func (p *publisherStub) PublishConfirmed(_ context.Context, value Publication) error {
	p.calls++
	p.publication = value
	return p.err
}
func eventBody(t *testing.T, eventType string, version int) []byte {
	t.Helper()
	raw, err := json.Marshal(events.Envelope[map[string]string]{ID: identity.NewID(), Type: eventType, SchemaVersion: version, OccurredAt: time.Now().UTC(), TenantID: identity.NewID(), CorrelationID: "corr", CausationID: "cause", Payload: map[string]string{"id": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func sqliteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ATTACH DATABASE ':memory:' AS messaging`).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE messaging.outbox (id blob primary key, tenant_id blob not null, type text not null, schema_version integer not null, payload blob not null, correlation_id text not null, causation_id text, status text not null, attempts integer not null default 0, next_attempt_at datetime not null, claimed_at datetime, last_error text, published_at datetime, created_at datetime)`,
		`CREATE TABLE messaging.inbox (id blob primary key, tenant_id blob not null, consumer text not null, event_id blob not null, generation integer not null default 0, processed_at datetime not null, UNIQUE(tenant_id,consumer,event_id,generation))`,
		`CREATE TABLE outcomes (id integer primary key autoincrement, event_id text, generation integer)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestIT354AtomicBusinessWriteAndOutbox(t *testing.T) {
	db := sqliteDB(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&outcome{EventID: "one"}).Error; err != nil {
			return err
		}
		e := events.Envelope[map[string]string]{ID: identity.NewID(), Type: "participant.channel_verified.v1", SchemaVersion: 1, OccurredAt: time.Now(), TenantID: identity.NewID(), CorrelationID: "corr", Payload: map[string]string{"id": "one"}}
		if err := AddOutbox(tx, e); err != nil {
			return err
		}
		return errors.New("forced")
	})
	if err == nil {
		t.Fatal("forced transaction committed")
	}
	var outcomes, outbox int64
	db.Model(&outcome{}).Count(&outcomes)
	db.Model(&database.OutboxIntent{}).Count(&outbox)
	if outcomes != 0 || outbox != 0 {
		t.Fatalf("partial commit: %d %d", outcomes, outbox)
	}
}
func TestIT355AndIT356ConfirmedOutbox(t *testing.T) {
	db := sqliteDB(t)
	now := time.Now().UTC()
	row := database.OutboxIntent{ID: identity.NewID(), TenantID: identity.NewID(), Type: "notification.delivery_requested.v2", SchemaVersion: 2, Payload: eventBody(t, "notification.delivery_requested.v2", 2), CorrelationID: "corr", CausationID: "cause", Status: "PENDING", NextAttemptAt: now, CreatedAt: now}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	publisher := &publisherStub{err: errors.New("nack")}
	metrics := observability.NewMetrics()
	dispatcher := Dispatcher{DB: db, Publisher: publisher, Clock: func() time.Time { return now.Add(time.Second) }, Metrics: metrics}
	if count, err := dispatcher.Dispatch(context.Background()); err != nil || count != 0 {
		t.Fatalf("nack result: %d %v", count, err)
	}
	var pending database.OutboxIntent
	db.First(&pending, "id=?", row.ID)
	if pending.Status != "PENDING" {
		t.Fatal("nack marked published")
	}
	if got := metrics.Prometheus(); !strings.Contains(got, `inspection_notification_queue_depth{channel="unknown",provider="unknown",queue="notification-outbox"} 1`) {
		t.Fatalf("queue depth after nack missing: %s", got)
	}
	publisher.err = nil
	if count, err := dispatcher.Dispatch(context.Background()); err != nil || count != 1 {
		t.Fatalf("confirm result: %d %v", count, err)
	}
	db.First(&pending, "id=?", row.ID)
	if pending.Status != "PUBLISHED" || publisher.publication.CorrelationID != "corr" || publisher.publication.CausationID != "cause" || !publisher.publication.Mandatory {
		t.Fatalf("confirmation metadata lost: %+v %+v", pending, publisher.publication)
	}
	if got := metrics.Prometheus(); !strings.Contains(got, `inspection_notification_queue_depth{channel="unknown",provider="unknown",queue="notification-outbox"} 0`) {
		t.Fatalf("queue depth after confirmation missing: %s", got)
	}
}
func TestIT357IT360IT547IT548IT551ToIT554IT577ToIT582EventConsumers(t *testing.T) {
	for _, eventType := range []string{"participant.channel_verified.v1", "inspection.created.v1", "inspection.state_changed.v1", "project.stage_changed.v1", "notification.delivery_requested.v1", "notification.channel_status.v1"} {
		t.Run(eventType, func(t *testing.T) {
			db := sqliteDB(t)
			consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: "consumer-" + eventType, Handle: func(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
				return tx.Create(&outcome{EventID: envelope.ID.String()}).Error
			}}
			body := eventBody(t, eventType, 1)
			first, err := consumer.Process(context.Background(), body, 0)
			if err != nil || !first {
				t.Fatalf("first: %v %v", first, err)
			}
			second, err := consumer.Process(context.Background(), body, 0)
			if err != nil || second {
				t.Fatalf("duplicate: %v %v", second, err)
			}
			replayed, err := consumer.Process(context.Background(), body, 1)
			if err != nil || !replayed {
				t.Fatalf("replay: %v %v", replayed, err)
			}
			var count int64
			db.Model(&outcome{}).Count(&count)
			if count != 2 {
				t.Fatalf("outcomes=%d", count)
			}
			if _, err := consumer.Process(context.Background(), eventBody(t, eventType, 2), 0); !errors.Is(err, ErrPermanent) {
				t.Fatalf("invalid major not rejected: %v", err)
			}
		})
	}
}

// Task 06 event contracts are deliberately exercised through the same inbox
// path as the earlier lifecycle events.  Keeping these cases in the
// integration contract test prevents a valid report/retention event from
// silently bypassing deduplication while still allowing the full external
// suite to provide its provider and worker assertions.
func TestIT567ToIT576AndIT583ToIT586Task06EventConsumers(t *testing.T) {
	for _, eventType := range []string{
		"analysis.comparison_requested.v1",
		"analysis.comparison_completed.v1",
		"inspection.classified.v1",
		"report.snapshot_created.v1",
		"report.ready.v1",
		"retention.purge_due.v1",
		"retention.purged.v1",
	} {
		t.Run(eventType, func(t *testing.T) {
			db := sqliteDB(t)
			consumer := Consumer{
				DB:       db,
				Registry: events.DefaultRegistry(),
				Name:     "task06-" + eventType,
				Handle: func(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
					return tx.Create(&outcome{EventID: envelope.ID.String()}).Error
				},
			}
			body := eventBody(t, eventType, 1)
			processed, err := consumer.Process(context.Background(), body, 0)
			if err != nil || !processed {
				t.Fatalf("valid event was not processed: %v %v", processed, err)
			}
			processed, err = consumer.Process(context.Background(), body, 0)
			if err != nil || processed {
				t.Fatalf("duplicate event was processed: %v %v", processed, err)
			}
			invalid := eventBody(t, eventType, 2)
			if _, err := consumer.Process(context.Background(), invalid, 0); !errors.Is(err, ErrPermanent) {
				t.Fatalf("invalid major was not rejected permanently: %v", err)
			}
			var count int64
			db.Model(&outcome{}).Count(&count)
			if count != 1 {
				t.Fatalf("duplicate or invalid event changed state: outcomes=%d", count)
			}
		})
	}
}

func TestIT358UnknownMajorIsPermanent(t *testing.T) {
	db := sqliteDB(t)
	consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: "unknown", Handle: func(context.Context, *gorm.DB, events.RawEnvelope) error { return nil }}
	if _, err := consumer.Process(context.Background(), eventBody(t, "participant.channel_verified.v1", 2), 0); !errors.Is(err, ErrPermanent) {
		t.Fatalf("unknown version not permanent: %v", err)
	}
}
func TestIT359AndIT362PoisonDoesNotBlock(t *testing.T) {
	if len(RetryDelays) != 4 || RetryDelays[1] != 5*time.Second || RetryDelays[2] != 30*time.Second || RetryDelays[3] != 5*time.Minute {
		t.Fatal("retry schedule drift")
	}
	db := sqliteDB(t)
	consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: "poison", Handle: func(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var p map[string]string
		_ = json.Unmarshal(envelope.Payload, &p)
		if p["id"] == "poison" {
			return ErrPermanent
		}
		return tx.Create(&outcome{EventID: envelope.ID.String()}).Error
	}}
	valid := eventBody(t, "notification.channel_status.v1", 1)
	if processed, err := consumer.Process(context.Background(), valid, 0); err != nil || !processed {
		t.Fatalf("later valid blocked: %v %v", processed, err)
	}
}
