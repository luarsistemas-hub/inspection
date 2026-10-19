//go:build integration

package resolvers

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_331ViewerCannotApplyReleaseHoldOrRequestPurge(t *testing.T) {
	fx := newAPI14RetentionFixture(t)
	viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.tenantID, Principal: requestctx.Principal{IdentityID: fx.identityID, TenantID: fx.tenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-331"})
	// An existing hold the viewer will try to release.
	holdID := identity.NewID()
	if err := fx.h.AdminDB.Create(&database.LegalHold{ID: holdID, TenantID: fx.tenantID, SourceMediaID: &fx.mediaID, Reason: "IT-331 existing hold", Active: true, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	operations := map[string]string{
		"applyLegalHold":        `mutation { applyLegalHold(input:{inspectionId:"` + fx.inspectionID.String() + `",reason:"IT-331",clientMutationId:"vv-it-331-apply"}) { status requestId clientMutationId } }`,
		"releaseLegalHold":      `mutation { releaseLegalHold(input:{sourceMediaId:"` + fx.mediaID.String() + `",reason:"IT-331",clientMutationId:"vv-it-331-release"}) { status requestId clientMutationId } }`,
		"recordDeletionRequest": api14RetentionMutation("recordDeletionRequest", fx.inspectionID.String(), "", "", "vv-it-331-purge"),
	}
	for name, query := range operations {
		t.Run(name, func(t *testing.T) {
			assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, query, viewer, "", ""), "FORBIDDEN")
		})
	}
	var holds []database.LegalHold
	if err := fx.h.AdminDB.Where("tenant_id=?", fx.tenantID).Find(&holds).Error; err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].ID != holdID || !holds[0].Active || holds[0].ReleasedAt != nil {
		t.Fatalf("viewer changed legal holds: %+v", holds)
	}
	var requests, purges int64
	if err := fx.h.AdminDB.Model(&database.DeletionRequest{}).Where("tenant_id=?", fx.tenantID).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.PurgeRun{}).Where("tenant_id=?", fx.tenantID).Count(&purges).Error; err != nil {
		t.Fatal(err)
	}
	var inspections int64
	if err := fx.h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.tenantID, fx.inspectionID).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 0 || purges != 0 || inspections != 1 {
		t.Fatalf("viewer purge attempt wrote state: deletionRequests=%d purgeRuns=%d inspection=%d", requests, purges, inspections)
	}
}
