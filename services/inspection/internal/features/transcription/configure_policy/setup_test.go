package configure_policy

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/core"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func policyDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS transcription").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE transcription.tenant_policies (tenant_id text PRIMARY KEY, enabled boolean, tenant_note_limit integer, tenant_audio_limit_ms integer, inspection_note_limit integer, inspection_audio_limit_ms integer, version integer, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE transcription.policy_mutation_receipts (tenant_id text,client_mutation_id text,payload_digest text,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,result_version integer,created_at datetime,PRIMARY KEY(tenant_id,client_mutation_id))`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSetupConfigurePolicyUT001(t *testing.T) {
	db := policyDB(t)
	tenant := identity.NewID()
	p := core.Policy{Enabled: true, TenantNoteLimit: 100, TenantAudioLimitMS: 300_000, InspectionNoteLimit: 20, InspectionAudioLimitMS: 30_000}
	if _, err := Setup(context.Background(), db, ConfigureInput{TenantID: tenant, Policy: p, ExpectedVersion: 0, ClientMutationID: "policy-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := Setup(context.Background(), db, ConfigureInput{TenantID: tenant, Policy: p, ExpectedVersion: 1, ClientMutationID: "policy-2"})
	if err != nil || got.Version != 2 || !got.Enabled {
		t.Fatalf("policy=%+v err=%v", got, err)
	}
}

func TestSetupRejectsEachZeroCeilingWithoutEnablingVoiceUT002(t *testing.T) {
	fields := []struct {
		name string
		zero func(*core.Policy)
	}{
		{"tenant note", func(p *core.Policy) { p.TenantNoteLimit = 0 }},
		{"tenant audio", func(p *core.Policy) { p.TenantAudioLimitMS = 0 }},
		{"inspection note", func(p *core.Policy) { p.InspectionNoteLimit = 0 }},
		{"inspection audio", func(p *core.Policy) { p.InspectionAudioLimitMS = 0 }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			db := policyDB(t)
			tenant := identity.NewID()
			p := core.Policy{Enabled: false, TenantNoteLimit: 100, TenantAudioLimitMS: 300_000, InspectionNoteLimit: 20, InspectionAudioLimitMS: 30_000}
			if _, err := Setup(context.Background(), db, ConfigureInput{TenantID: tenant, Policy: p, ExpectedVersion: 0}); err != nil {
				t.Fatal(err)
			}
			field.zero(&p)
			p.Enabled = true
			if _, err := Setup(context.Background(), db, ConfigureInput{TenantID: tenant, Policy: p, ExpectedVersion: 1}); !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("zero ceiling error=%v", err)
			}
			var enabled bool
			var version int
			if err := db.Raw("SELECT enabled,version FROM transcription.tenant_policies WHERE tenant_id=?", tenant.String()).Row().Scan(&enabled, &version); err != nil {
				t.Fatal(err)
			}
			if enabled || version != 1 {
				t.Fatalf("invalid policy enabled voice or changed version: enabled=%v version=%d", enabled, version)
			}
		})
	}
}

func TestSetupPolicyMutationIsIdempotent(t *testing.T) {
	db := policyDB(t)
	tenant := identity.NewID()
	p := core.Policy{Enabled: true, TenantNoteLimit: 100, TenantAudioLimitMS: 300_000, InspectionNoteLimit: 20, InspectionAudioLimitMS: 30_000}
	in := ConfigureInput{TenantID: tenant, Policy: p, ExpectedVersion: 0, ClientMutationID: "same-request"}
	first, err := Setup(context.Background(), db, in)
	if err != nil || first.Version != 1 {
		t.Fatalf("first policy=%+v err=%v", first, err)
	}
	retry, err := Setup(context.Background(), db, in)
	if err != nil || retry.Version != 1 {
		t.Fatalf("retry policy=%+v err=%v", retry, err)
	}
	changed := in
	changed.Policy.TenantNoteLimit++
	if _, err := Setup(context.Background(), db, changed); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("mutation ID reused with different payload: %v", err)
	}
	var count int
	if err := db.Raw("SELECT count(*) FROM transcription.tenant_policies WHERE tenant_id=?", tenant.String()).Scan(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate write count=%d err=%v", count, err)
	}
}
