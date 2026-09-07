// Package render_pdf renders immutable snapshots and records private PDF
// artifacts without altering classification or the canonical HTML snapshot.
package render_pdf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/pdf"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	Renderer pdf.Renderer
	Store    objectstore.Store
	Now      func() time.Time
}

func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.Renderer == nil {
		return nil, fmt.Errorf("reports/render_pdf: missing renderer")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			SnapshotID identity.ID `json:"snapshotId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.SnapshotID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var snapshot database.ReportSnapshot
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.SnapshotID).First(&snapshot).Error; err != nil {
			return err
		}
		var existing database.ReportArtifact
		if err := tx.Where("tenant_id=? AND snapshot_id=? AND kind IN ?", envelope.TenantID, snapshot.ID, []string{"PDF", "HTML"}).First(&existing).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var html string
		if err := json.Unmarshal(snapshot.HTML, &html); err != nil {
			html = string(snapshot.HTML)
		}
		reader, err := deps.Renderer.Render(ctx, strings.NewReader(html), pdf.AssetBundle{})
		if err != nil {
			if messaging.HasAttempt(ctx) && messaging.Attempt(ctx) < 3 {
				return err
			}
			return createHTMLFallback(ctx, deps.Store, tx, envelope, snapshot, deps.Now().UTC())
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(reader, 64<<20+1))
		if err != nil {
			return err
		}
		if len(data) == 0 || len(data) > 64<<20 {
			return fmt.Errorf("report PDF exceeds limit")
		}
		if deps.Store.Client == nil {
			return createHTMLFallback(ctx, deps.Store, tx, envelope, snapshot, deps.Now().UTC())
		}
		key, digest, err := deps.Store.PutDerivative(ctx, envelope.TenantID, snapshot.ID, "report-pdf", "application/pdf", data)
		if err != nil {
			return err
		}
		now := deps.Now().UTC()
		artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: envelope.TenantID, SnapshotID: snapshot.ID, Kind: "PDF", ObjectKey: key, SHA256: digest, Status: "READY", CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
			return err
		}
		return emitReady(tx, envelope, snapshot, artifact, now)
	}, nil
}

func createHTMLFallback(ctx context.Context, store objectstore.Store, tx *gorm.DB, envelope events.RawEnvelope, snapshot database.ReportSnapshot, now time.Time) error {
	var html string
	if err := json.Unmarshal(snapshot.HTML, &html); err != nil {
		html = string(snapshot.HTML)
	}
	objectKey := fmt.Sprintf("snapshot/%s.html", snapshot.ID)
	digest := snapshot.HTMLDigest
	if store.Client != nil {
		key, storedDigest, err := store.PutDerivative(ctx, envelope.TenantID, snapshot.ID, "report-html", "text/html", []byte(html))
		if err != nil {
			return err
		}
		objectKey, digest = key, storedDigest
	}
	artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: envelope.TenantID, SnapshotID: snapshot.ID, Kind: "HTML", ObjectKey: objectKey, SHA256: digest, Status: "READY", CreatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
		return err
	}
	return emitReady(tx, envelope, snapshot, artifact, now)
}

func emitReady(tx *gorm.DB, envelope events.RawEnvelope, snapshot database.ReportSnapshot, artifact database.ReportArtifact, now time.Time) error {
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "report.ready.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: snapshot.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"snapshotId": snapshot.ID, "inspectionId": snapshot.InspectionID, "kind": artifact.Kind, "status": artifact.Status}})
}
