//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"

	"gorm.io/gorm"
)

func TestVV_IT_074CreateVideoFixedOriginInspectionIsAtomic(t *testing.T) {
	fx := newFixedOriginGraphQLFixture(t, false)
	setHistoricalVideoAdmissionDisabled(t, fx)
	originVersionID := seedFixedVideoOrigin(t, fx)
	response := executeFixedOriginEntryWithReference(t, fx, "createInspection", "vv-it-074", originVersionID.String())
	var created struct {
		Data struct {
			Create struct {
				Inspection *struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &created); err != nil || created.Data.Create.Inspection == nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("createInspection(VIDEO,FIXED_ORIGIN,O1) = %s (%v)", response, err)
	}
	inspectionID, err := identity.ParseID(created.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, inspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		var snapshots []database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Find(&snapshots).Error; err != nil {
			return err
		}
		if inspection.MediaKind != "VIDEO" || len(snapshots) != 1 || snapshots[0].ComparisonMode != string(catalog.FixedOrigin) || snapshots[0].ReferenceVersionID == nil || *snapshots[0].ReferenceVersionID != originVersionID {
			t.Fatalf("persisted inspection=%s snapshots=%+v; want VIDEO pinned to FIXED_ORIGIN O1", inspection.MediaKind, snapshots)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A failure inside the creation transaction leaves nothing behind.
	if err := testfailure.Install(fx.h.DB); err != nil {
		t.Fatal(err)
	}
	failed := executeFixedOriginRequest(t, fx, fixedOriginEntryQuery(fx, "createInspection", "vv-it-074-failed", originVersionID.String()), fx.ctx, "createInspection", "internal")
	assertFixedOriginTransportError(t, failed, "INTERNAL")
	assertFixedOriginNoSideEffects(t, fx, "vv-it-074-failed")
	var orphanSnapshots int64
	if err := fx.h.AdminDB.Model(&database.ReferenceSnapshot{}).Where("tenant_id=? AND inspection_id NOT IN (SELECT id FROM inspections.inspections WHERE tenant_id=?)", fx.seed.TenantID, fx.seed.TenantID).Count(&orphanSnapshots).Error; err != nil {
		t.Fatal(err)
	}
	if orphanSnapshots != 0 {
		t.Fatalf("failed creation left %d reference snapshots", orphanSnapshots)
	}
}

func TestVV_IT_155ViewerCannotRetryPreparationOrRecoverDeletion(t *testing.T) {
	t.Run("retryVideoPreparation", func(t *testing.T) {
		fx := newAPI08Fixture(t)
		resources := seedAPI08Resources(t, fx)
		viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-155"})
		assertFixedOriginTransportError(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, resources.query(fx, "retryVideoPreparation", "vv-it-155-retry"), viewer, "", ""), "FORBIDDEN")
		resources.assertUnchanged(t, fx)
	})
	t.Run("deletion recovery", func(t *testing.T) {
		fx := newAPI14RetentionFixture(t)
		viewer := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.tenantID, Principal: requestctx.Principal{IdentityID: fx.identityID, TenantID: fx.tenantID, Roles: []string{auth.Viewer}}, CorrelationID: "vv-it-155"})
		assertFixedOriginTransportError(t, executeAPI14Retention(t, fx, api14RetentionMutation("recordDeletionRequest", "", fx.mediaID.String(), "", "vv-it-155-delete"), viewer, "", ""), "FORBIDDEN")
		assertAPI14RetentionNoWrites(t, fx)
	})
}

func TestVV_IT_151FailedPreparationStaysListedForCleanup(t *testing.T) {
	fx := newAPI08Fixture(t)
	failed := seedAPI08Preparation(t, fx, "FAILED", 2, "RETAINED")
	manager := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: fx.seed.TenantID, Roles: []string{auth.Manager}}, CorrelationID: "vv-it-151"})
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { videoPreparations(status:["FAILED"]) { nodes { mediaId status retryable } } }`, manager, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"mediaId":"`+failed.mediaID.String()+`"`) || !strings.Contains(response, `"status":"FAILED"`) {
		t.Fatalf("unresolved failed C1 is not listed for cleanup: %s", response)
	}
}

// TestVV_IT_347DirectIntegratorRejectsSecondLiveMainAttempt exercises the
// same GraphQL beginVideoCapture boundary used by a direct integrator. The
// API03 transport case seeds a live CURRENT attempt for R1 and requires the
// next attempt to fail with CONFLICT.
func TestVV_IT_347DirectIntegratorRejectsSecondLiveMainAttempt(t *testing.T) {
	TestAPI03CompetingAttemptRejectedIT406(t)
}

// TestDirectFixedOriginVideoRejectsPhotoBaseline delegates to the GraphQL
// matrix that enforces the catalog-first error for cross-modality baselines.
func TestDirectFixedOriginVideoRejectsPhotoBaseline(t *testing.T) {
	TestFixedOriginPhotoReferenceForVideoGraphQL(t)
}
