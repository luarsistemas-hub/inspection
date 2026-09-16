package request

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/postgres"
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
	rich, err := catalog.Resolve(core.TemplateRef{Name: "capture-link", Version: "v2"}, core.ChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	richRendered, err := rich.Render(map[string]string{
		"captureUrl":    "https://capture.test/a",
		"recipientName": "Ana",
		"assetName":     "Apartamento 101",
		"assetAddress":  "Rua das Flores, 10 <script>",
		"expiresAt":     "15/09/2026 às 18:00",
	})
	if err != nil || !strings.Contains(richRendered.Text, "Ana") || !strings.Contains(richRendered.Text, "Apartamento 101") || !strings.Contains(richRendered.Text, "Rua das Flores, 10") || !strings.Contains(richRendered.Text, "15/09/2026 às 18:00") {
		t.Fatalf("rich capture render: %#v %v", richRendered, err)
	}
	if !strings.Contains(richRendered.HTML, "&lt;script&gt;") {
		t.Fatalf("rich capture HTML must escape values: %s", richRendered.HTML)
	}
	reminder, err := catalog.Resolve(core.TemplateRef{Name: "reminder", Version: "v1"}, core.ChannelSMS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reminder.Render(map[string]string{"captureUrl": "https://capture.test/a", "recipientName": "Ana", "password": "not-rendered"}); !core.Is(err, core.IncompatibleVariables) {
		t.Fatalf("undeclared variable: %v", err)
	}
}

func TestCaptureLinkNotificationUsesRichRevisionOnlyForEmail(t *testing.T) {
	emailTemplate, emailVariables := core.CaptureLinkNotification(core.ChannelEmail, "Ana", "Apartamento 101", "Rua das Flores, 10", time.Date(2026, 9, 15, 18, 0, 0, 0, time.FixedZone("BRT", -3*60*60)))
	if emailTemplate.Version != "v2" || emailVariables["recipientName"] != "Ana" || emailVariables["assetName"] != "Apartamento 101" {
		t.Fatalf("email notification: %#v %#v", emailTemplate, emailVariables)
	}

	smsTemplate, smsVariables := core.CaptureLinkNotification(core.ChannelSMS, "Ana", "Apartamento 101", "Rua das Flores, 10", time.Now())
	if smsTemplate.Version != "v1" || len(smsVariables) != 1 || smsVariables["recipientName"] != "Ana" {
		t.Fatalf("sms notification: %#v %#v", smsTemplate, smsVariables)
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

func TestDeliveryIdempotencyConflictTargetsPartialIndex(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  "host=localhost user=inspection dbname=inspection sslmode=disable",
		PreferSimpleProtocol: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}

	statement := db.Clauses(deliveryIdempotencyConflict()).Create(&database.Delivery{
		ID: identity.NewID(), TenantID: identity.NewID(), IntentID: identity.NewID(),
	})
	if statement.Error != nil {
		t.Fatal(statement.Error)
	}
	sql := statement.Statement.SQL.String()
	if !strings.Contains(sql, `ON CONFLICT ("tenant_id","idempotency_key")  WHERE idempotency_key <> '' DO NOTHING`) {
		t.Fatalf("partial-index predicate missing from conflict target: %s", sql)
	}
}
