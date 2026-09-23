package messaging

import (
	"context"
	"errors"
	"testing"

	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
)

type transactionObserverStub struct {
	values []observability.TransactionObservation
}

func (s *transactionObserverStub) AfterTransaction(_ context.Context, _ events.RawEnvelope, observation observability.TransactionObservation) {
	s.values = append(s.values, observation)
}

func TestConsumerObserverRunsAfterTransactionAndSeesDuplicates(t *testing.T) {
	db := sqliteDB(t)
	observer := &transactionObserverStub{}
	fail := true
	consumer := Consumer{
		DB: db, Registry: events.DefaultRegistry(), Name: "observer-test", Observer: observer,
		Handle: func(_ context.Context, tx *gorm.DB, _ events.RawEnvelope) error {
			if fail {
				return errors.New("rollback")
			}
			return tx.Create(&outcome{EventID: "committed"}).Error
		},
	}
	body := eventBody(t, "analysis.comparison_requested.v1", 1)
	if _, err := consumer.Process(context.Background(), body, 0); err == nil {
		t.Fatal("rollback was accepted")
	}
	if len(observer.values) != 1 || observer.values[0].Outcome != "error" || observer.values[0].Phase != "rollback" {
		t.Fatalf("rollback observation=%+v", observer.values)
	}
	fail = false
	if processed, err := consumer.Process(context.Background(), body, 0); err != nil || !processed {
		t.Fatalf("commit failed: processed=%v err=%v", processed, err)
	}
	if processed, err := consumer.Process(context.Background(), body, 0); err != nil || processed {
		t.Fatalf("duplicate changed state: processed=%v err=%v", processed, err)
	}
	if len(observer.values) != 3 || observer.values[1].Outcome != "completed" || observer.values[1].Phase != "committed" || observer.values[2].Outcome != "duplicate" || observer.values[2].Phase != "committed" {
		t.Fatalf("transaction observations=%+v", observer.values)
	}
}
