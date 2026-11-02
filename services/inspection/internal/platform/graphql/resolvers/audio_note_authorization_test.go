package resolvers

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAudioNoteIdentityRequiresInspectionScopeForInternalReaders(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "inspections", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE transcription.audio_notes (tenant_id text,id text,inspection_id text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE inspections.inspections (tenant_id text,id text,business_unit_id text,asset_id text,project_id text)`).Error; err != nil {
		t.Fatal(err)
	}
	tenantID, inspectionID, noteID := identity.NewID(), identity.NewID(), identity.NewID()
	unitID, assetID := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections VALUES (?,?,?,?,NULL)`, tenantID, inspectionID, unitID, assetID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.audio_notes VALUES (?,?,?)`, tenantID, noteID, inspectionID).Error; err != nil {
		t.Fatal(err)
	}
	r := &Resolver{DB: db}
	ctx := func(roles []string, scopes []requestctx.Scope) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{
			TenantID:  tenantID,
			Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Internal: true, Roles: roles, Scopes: scopes},
		})
	}
	if _, _, err := audioNoteIdentity(ctx([]string{auth.Manager}, []requestctx.Scope{{Kind: "ASSET", ID: assetID}}), r, noteID.String()); err != nil {
		t.Fatalf("matching inspection asset scope was rejected: %v", err)
	}
	if _, _, err := audioNoteIdentity(ctx([]string{auth.Manager}, []requestctx.Scope{{Kind: "ASSET", ID: identity.NewID()}}), r, noteID.String()); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("unscoped note read should be forbidden, got %v", err)
	}
	if _, _, err := audioNoteIdentity(ctx([]string{auth.CustomerViewer}, nil), r, noteID.String()); !apperror.Is(err, apperror.Forbidden) {
		t.Fatalf("role without internal note access should be forbidden, got %v", err)
	}
}

func TestAudioNoteCaptureMetadataIsNotAnOperator(t *testing.T) {
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{CorrelationID: "capture-session"})
	ctx = requestctx.WithExternalCredentials(ctx, requestctx.ExternalCredentials{SessionToken: "session", CSRFToken: "csrf"})
	if audioNoteHasOperator(ctx) {
		t.Fatal("ordinary HTTP metadata must not classify an authorized capture as an operator")
	}
	if !audioNoteIsParticipant(ctx) {
		t.Fatal("an authorized capture session must be treated as the participant")
	}

	operator := requestctx.WithMetadata(context.Background(), requestctx.Metadata{Principal: requestctx.Principal{IdentityID: identity.NewID()}})
	if !audioNoteHasOperator(operator) || audioNoteIsParticipant(operator) {
		t.Fatal("an authenticated operator must not be treated as the capture participant")
	}

	internal := requestctx.WithMetadata(context.Background(), requestctx.Metadata{Principal: requestctx.Principal{Internal: true}})
	if !audioNoteHasOperator(internal) || audioNoteIsParticipant(internal) {
		t.Fatal("trusted internal orchestration must not be treated as the capture participant")
	}
}

func TestAudioNoteUserErrorsPreserveSafeMessagesUT022(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code apperror.Code
		want string
	}{
		{"dependency message", apperror.New(apperror.DependencyUnavailable, "", "Não foi possível reenviar o convite agora."), apperror.DependencyUnavailable, "Não foi possível reenviar o convite agora."},
		{"dependency without message", apperror.Wrap(apperror.DependencyUnavailable, errors.New("provider unavailable")), apperror.DependencyUnavailable, "Não foi possível concluir a operação."},
		{"session expiry message", apperror.New(apperror.SessionExpired, "", "A sessão de captura expirou."), apperror.SessionExpired, "A sessão de captura expirou."},
		{"internal", apperror.Wrap(apperror.Internal, errors.New("provider token leaked")), apperror.Internal, "Não foi possível concluir a operação."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userError := userErrorFrom(tc.err)
			if userError.Code != string(tc.code) || userError.Message != tc.want || userError.Field != nil {
				t.Fatalf("unexpected public error: %+v", userError)
			}
		})
	}
}
