//go:build integration

package resolvers

import (
	"context"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestAudioNoteReadScopeIT314(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{MediaKind: "VIDEO", RequirementKey: "room-1", SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	noteID := identity.NewID()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,video_attempt_id,start_ms,end_ms,time_basis,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, f.TenantID, noteID, f.InspectionID, f.ResponsibilityID, noteID, "room-1", "voice-v1", "pt-BR-1", "VIDEO_INTERVAL", f.VideoAttemptID, 0, 1000, "CAPTURE_CLOCK", 1000, strings.Repeat("a", 64), "audio/webm", 1024, "COMPLETED", "VALID").Error
	}); err != nil {
		t.Fatal(err)
	}
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.TenantID, Audience: "inspection-dashboard", Product: auth.DashboardProduct, ProductEntitlements: []string{auth.DashboardProduct}, Roles: []string{auth.Viewer}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: identity.NewID()}}}
	readCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: principal})
	r := &Resolver{DB: h.DB, WithinTenant: h.WithinTenant, Authorizer: auth.Authorizer{}}
	_, _, err = audioNoteIdentity(readCtx, r, noteID.String())
	if !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("out-of-scope viewer read error=%v, want FORBIDDEN", err)
	}
}
