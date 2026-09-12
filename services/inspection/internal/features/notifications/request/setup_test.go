package request

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupRequiresDatabase(t *testing.T) {
	if _, err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing database accepted")
	}
}

func TestValidationUT001ToUT006(t *testing.T) {
	valid := core.Notification{TenantID: identity.NewID(), Recipient: core.Recipient{Destination: "recipient@example.test"}, Channel: core.ChannelEmail, Template: core.TemplateRef{Name: "capture-link", Version: "v1"}, Variables: map[string]string{"captureUrl": "https://capture.test/a", "recipientName": "Ana"}, CorrelationID: "corr-1", IdempotencyKey: "invite-1:email"}
	if err := core.Validate(valid, core.DefaultCatalog()); err != nil {
		t.Fatal(err)
	}
	zero := valid
	zero.TenantID = identity.ID{}
	if err := core.Validate(zero, core.DefaultCatalog()); !core.Is(err, core.InvalidInput) {
		t.Fatalf("tenant validation: %v", err)
	}
	channel := valid
	channel.Channel = "PUSH"
	if err := core.Validate(channel, core.DefaultCatalog()); !core.Is(err, core.UnknownChannel) {
		t.Fatalf("channel validation: %v", err)
	}
	recipient := valid
	recipient.Recipient.Destination = ""
	if err := core.Validate(recipient, core.DefaultCatalog()); !core.Is(err, core.InvalidRecipient) {
		t.Fatalf("recipient validation: %v", err)
	}
	a, err := core.CanonicalDigest(valid)
	if err != nil {
		t.Fatal(err)
	}
	valid.Variables = map[string]string{"recipientName": "Ana", "captureUrl": "https://capture.test/a"}
	b, err := core.CanonicalDigest(valid)
	if err != nil || a != b {
		t.Fatalf("digest must be stable: %q %q %v", a, b, err)
	}
	valid.IdempotencyKey = string(make([]byte, 201))
	if err := core.Validate(valid, core.DefaultCatalog()); !core.Is(err, core.InvalidInput) {
		t.Fatalf("idempotency boundary: %v", err)
	}
}

func TestCatalogUT007ToUT012(t *testing.T) {
	catalog := core.DefaultCatalog()
	template, err := catalog.Resolve(core.TemplateRef{Name: "capture-link", Version: "v1"}, core.ChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := template.Render(map[string]string{"captureUrl": "https://capture.test/a", "recipientName": "Ana"})
	if err != nil || rendered.Subject == "" || rendered.Text == "" || rendered.HTML == "" {
		t.Fatalf("capture render: %#v %v", rendered, err)
	}
	if _, err := catalog.Resolve(core.TemplateRef{Name: "unknown", Version: "v1"}, core.ChannelEmail); !core.Is(err, core.UnknownTemplate) {
		t.Fatalf("unknown template: %v", err)
	}
	if _, err := template.Render(map[string]string{"recipientName": "Ana"}); !core.Is(err, core.MissingVariable) {
		t.Fatalf("missing variable: %v", err)
	}
	reminder, err := catalog.Resolve(core.TemplateRef{Name: "reminder", Version: "v1"}, core.ChannelSMS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reminder.Render(map[string]string{"captureUrl": "https://capture.test/a", "recipientName": "Ana", "password": "not-rendered"}); !core.Is(err, core.IncompatibleVariables) {
		t.Fatalf("undeclared variable: %v", err)
	}
}

func TestServiceUsesAmbientTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Send(context.Background(), core.Notification{}); !core.Is(err, core.InvalidInput) {
		t.Fatalf("invalid request: %v", err)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("unexpected persistence error")
	}
}

func TestServiceBlocksV2ProductionUntilRolloutFlagIsEnabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := Setup(Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	notification := core.Notification{
		TenantID:       identity.NewID(),
		Recipient:      core.Recipient{Destination: "recipient@example.test"},
		Channel:        core.ChannelEmail,
		Template:       core.TemplateRef{Name: "capture-link", Version: "v1"},
		Variables:      map[string]string{"captureUrl": "https://capture.test/a", "recipientName": "Ana"},
		CorrelationID:  "corr-rollout",
		IdempotencyKey: "rollout-1:email",
	}
	_, err = service.Send(context.Background(), notification)
	if !errors.Is(err, ErrV2ProducersDisabled) {
		t.Fatal("v2 request was not blocked while rollout flag was disabled")
	}
}
