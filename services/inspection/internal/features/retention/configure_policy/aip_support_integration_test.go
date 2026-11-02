//go:build integration

package configure_policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// aipHarness opens the isolated PostgreSQL/RabbitMQ/MinIO harness. The runtime
// role (forced RLS) is the connection used by the code under test.
func aipHarness(t *testing.T) *harness.Harness {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	h, err := harness.NewFromEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// aipInspection identifies every durable artifact class one closed inspection
// owns, so a purge can be asserted class by class.
type aipInspection struct {
	ID, ResponsibilityID, MediaID, DerivativeID, SnapshotID, ArtifactID identity.ID
	MediaKey, DerivativeKey, ArtifactKey                                string
}

// newClosedInspection adds a terminal inspection to the fixture tenant with a
// private original, a derivative, an immutable report snapshot and a report
// artifact, all stored in MinIO. closedAt is the authoritative closure instant
// (inspection.updated_at) that the retention clock starts from.
func newClosedInspection(t *testing.T, h *harness.Harness, base harness.Task06Fixture, closedAt time.Time) aipInspection {
	t.Helper()
	ctx := context.Background()
	item := aipInspection{ID: identity.NewID(), ResponsibilityID: identity.NewID(), MediaID: identity.NewID(), DerivativeID: identity.NewID(), SnapshotID: identity.NewID(), ArtifactID: identity.NewID()}
	item.MediaKey = "tenant/" + base.TenantID.String() + "/media/" + item.MediaID.String()
	item.DerivativeKey = item.MediaKey + "/normalized"
	item.ArtifactKey = "tenant/" + base.TenantID.String() + "/reports/" + item.SnapshotID.String() + ".pdf"
	for key, payload := range map[string][]byte{item.MediaKey: []byte("aip-original"), item.DerivativeKey: []byte("aip-derivative"), item.ArtifactKey: []byte("%PDF-aip")} {
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
			t.Fatalf("seed object %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for _, key := range []string{item.MediaKey, item.DerivativeKey, item.ArtifactKey} {
			_ = h.Store.Delete(context.Background(), key)
		}
	})
	var template database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", base.TenantID, base.InspectionID).Take(&template).Error; err != nil {
		t.Fatal(err)
	}
	now := closedAt.UTC().Truncate(time.Microsecond)
	err := h.WithinTenant(ctx, base.TenantID, func(tx *gorm.DB) error {
		inspection := template
		inspection.ID, inspection.SourceKey, inspection.Status, inspection.CreatedAt, inspection.UpdatedAt = item.ID, "aip-"+item.ID.String(), "COMPLETED", now, now
		// A standalone inspection counts from its own closure; the fixture's
		// project is ACTIVE, which would keep it outside retention.
		inspection.ProjectID, inspection.StageID = nil, nil
		if err := tx.Create(&inspection).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: item.ResponsibilityID, TenantID: base.TenantID, InspectionID: item.ID, ParticipantID: base.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: item.MediaID, TenantID: base.TenantID, ResponsibilityID: item.ResponsibilityID, ObjectKey: item.MediaKey, ContentType: "image/png", SHA256: "aip-" + item.MediaID.String(), SizeBytes: 12, Status: "READY", MediaKind: "PHOTO", IdempotencyKey: "aip-media-" + item.MediaID.String(), RequirementKey: "aip", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaDerivative{ID: item.DerivativeID, TenantID: base.TenantID, MediaID: item.MediaID, ObjectKey: item.DerivativeKey, Kind: "NORMALIZED", SHA256: "aip-d-" + item.MediaID.String(), ContentType: "image/png", Width: 1, Height: 1, SizeBytes: 14, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ReportSnapshot{ID: item.SnapshotID, TenantID: base.TenantID, InspectionID: item.ID, Mode: "HISTORICAL", Classification: "NORMAL", JSONDigest: "aip-j", HTMLDigest: "aip-h", VersionNumber: 1, CanonicalJSON: json.RawMessage(`{}`), HTML: json.RawMessage(`"<p>aip</p>"`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ReportArtifact{ID: item.ArtifactID, TenantID: base.TenantID, SnapshotID: item.SnapshotID, Kind: "PDF", ObjectKey: item.ArtifactKey, SHA256: "aip-pdf", Status: "READY", CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatalf("seed closed inspection: %v", err)
	}
	return item
}

// closeBaseInspection moves the fixture inspection to a terminal state closed
// at closedAt without touching any other column.
func closeBaseInspection(t *testing.T, h *harness.Harness, base harness.Task06Fixture, closedAt time.Time) {
	t.Helper()
	setClosure(t, h, base.TenantID, base.InspectionID, closedAt, "COMPLETED")
}

// setClosure rewrites the authoritative closure instant and status of a
// standalone inspection (it leaves the fixture's active project).
func setClosure(t *testing.T, h *harness.Harness, tenantID, inspectionID identity.ID, closedAt time.Time, status string) {
	t.Helper()
	if err := h.AdminDB.Exec("UPDATE inspections.inspections SET updated_at=?, status=?, project_id=NULL, stage_id=NULL WHERE tenant_id=? AND id=?", closedAt.UTC(), status, tenantID, inspectionID).Error; err != nil {
		t.Fatal(err)
	}
}

// dueEnvelope builds the retention.purge_due.v1 event that the scheduler emits.
func dueEnvelope(t *testing.T, tenantID, inspectionID identity.ID) events.RawEnvelope {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"inspectionId": inspectionID})
	if err != nil {
		t.Fatal(err)
	}
	return events.RawEnvelope{ID: identity.NewID(), Type: "retention.purge_due.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "aip-retention-" + inspectionID.String(), Payload: payload}
}

func objectExists(ctx context.Context, h *harness.Harness, key string) (bool, error) {
	_, err := h.Store.Client.Head(ctx, h.Store.Bucket, key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, objectstore.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// aipPresence counts what remains of one inspection in each data class.
type aipPresence struct {
	Inspections, Responsibilities, Media, Derivatives, Snapshots, Artifacts, PurgeRuns int64
	Original, Derivative, Report                                                       bool
}

func presenceOf(t *testing.T, h *harness.Harness, tenantID identity.ID, item aipInspection) aipPresence {
	t.Helper()
	ctx := context.Background()
	var p aipPresence
	for _, count := range []struct {
		dest  *int64
		model any
		where string
		arg   identity.ID
	}{
		{&p.Inspections, &database.Inspection{}, "tenant_id=? AND id=?", item.ID},
		{&p.Responsibilities, &database.Responsibility{}, "tenant_id=? AND id=?", item.ResponsibilityID},
		{&p.Media, &database.MediaObject{}, "tenant_id=? AND id=?", item.MediaID},
		{&p.Derivatives, &database.MediaDerivative{}, "tenant_id=? AND id=?", item.DerivativeID},
		{&p.Snapshots, &database.ReportSnapshot{}, "tenant_id=? AND id=?", item.SnapshotID},
		{&p.Artifacts, &database.ReportArtifact{}, "tenant_id=? AND id=?", item.ArtifactID},
		{&p.PurgeRuns, &database.PurgeRun{}, "tenant_id=? AND inspection_id=?", item.ID},
	} {
		if err := h.AdminDB.Model(count.model).Where(count.where, tenantID, count.arg).Count(count.dest).Error; err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if p.Original, err = objectExists(ctx, h, item.MediaKey); err != nil {
		t.Fatal(err)
	}
	if p.Derivative, err = objectExists(ctx, h, item.DerivativeKey); err != nil {
		t.Fatal(err)
	}
	if p.Report, err = objectExists(ctx, h, item.ArtifactKey); err != nil {
		t.Fatal(err)
	}
	return p
}

// wantFullyRetained fails when any class of the inspection is missing.
func wantFullyRetained(t *testing.T, label string, p aipPresence) {
	t.Helper()
	if p.Inspections != 1 || p.Responsibilities != 1 || p.Media != 1 || p.Derivatives != 1 || p.Snapshots != 1 || p.Artifacts != 1 || !p.Original || !p.Derivative || !p.Report || p.PurgeRuns != 0 {
		t.Fatalf("%s: data was removed before retention elapsed: %+v", label, p)
	}
}

// wantFullyPurged fails when any class survived the purge.
func wantFullyPurged(t *testing.T, label string, p aipPresence) {
	t.Helper()
	if p.Inspections != 0 || p.Responsibilities != 0 || p.Media != 0 || p.Derivatives != 0 || p.Snapshots != 0 || p.Artifacts != 0 || p.Original || p.Derivative || p.Report || p.PurgeRuns != 1 {
		t.Fatalf("%s: purge left data behind: %+v", label, p)
	}
}

// flakyStore delegates to the real object store but fails the first
// failures deletes of one key, simulating an outage after partial cleanup.
type flakyStore struct {
	objectstore.Client
	failKey  string
	failures atomic.Int32
}

func newFlakyStore(client objectstore.Client, failKey string, failures int32) *flakyStore {
	store := &flakyStore{Client: client, failKey: failKey}
	store.failures.Store(failures)
	return store
}

func (s *flakyStore) Delete(ctx context.Context, bucket, key string) error {
	if key == s.failKey && s.failures.Add(-1) >= 0 {
		return errors.New("simulated object store outage")
	}
	return s.Client.(objectstore.Deleter).Delete(ctx, bucket, key)
}
