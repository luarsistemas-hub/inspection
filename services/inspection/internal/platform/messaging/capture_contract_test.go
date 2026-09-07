package messaging

import (
	"context"
	"errors"
	"testing"

	"inspection/services/inspection/internal/contracts/events"

	"gorm.io/gorm"
)

func TestTask5EventsIT549ToIT550IT555ToIT560IT563ToIT566(t *testing.T) {
	cases := []struct {
		validID, invalidID, eventType string
	}{
		{"IT-549", "IT-550", "origin.invitation_requested.v1"},
		{"IT-555", "IT-556", "media.upload_completed.v1"},
		{"IT-557", "IT-558", "media.verified.v1"},
		{"IT-559", "IT-560", "media.screened.v1"},
		{"IT-563", "IT-564", "recapture.requested.v1"},
		{"IT-565", "IT-566", "recapture.completed.v1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.validID+" valid event produces one local outcome", func(t *testing.T) {
			db := sqliteDB(t)
			consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: "task5-" + testCase.eventType, Handle: func(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
				return tx.Create(&outcome{EventID: envelope.ID.String()}).Error
			}}
			body := eventBody(t, testCase.eventType, 1)
			processed, err := consumer.Process(context.Background(), body, 0)
			if err != nil || !processed {
				t.Fatalf("valid event rejected: processed=%v err=%v", processed, err)
			}
			var count int64
			if err := db.Model(&outcome{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("local outcome count=%d err=%v", count, err)
			}
		})
		t.Run(testCase.invalidID+" duplicate and invalid major are safe", func(t *testing.T) {
			db := sqliteDB(t)
			consumer := Consumer{DB: db, Registry: events.DefaultRegistry(), Name: "task5-" + testCase.eventType, Handle: func(_ context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
				return tx.Create(&outcome{EventID: envelope.ID.String()}).Error
			}}
			body := eventBody(t, testCase.eventType, 1)
			if processed, err := consumer.Process(context.Background(), body, 0); err != nil || !processed {
				t.Fatalf("fixture event rejected: processed=%v err=%v", processed, err)
			}
			processed, err := consumer.Process(context.Background(), body, 0)
			if err != nil || processed {
				t.Fatalf("duplicate event changed state: processed=%v err=%v", processed, err)
			}
			if _, err := consumer.Process(context.Background(), eventBody(t, testCase.eventType, 2), 0); !errors.Is(err, ErrPermanent) {
				t.Fatalf("invalid major was not permanent: %v", err)
			}
			var count int64
			if err := db.Model(&outcome{}).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("invalid or duplicate event changed outcome: count=%d err=%v", count, err)
			}
		})
	}
}
