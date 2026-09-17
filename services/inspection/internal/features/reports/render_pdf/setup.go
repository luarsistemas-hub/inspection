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
	reportcore "inspection/services/inspection/internal/features/reports/core"
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
		var value reportcore.Snapshot
		if err := json.Unmarshal(snapshot.CanonicalJSON, &value); err != nil {
			return messaging.ErrPermanent
		}
		assets, availability, err := loadDisplayAssets(ctx, tx, deps.Store, envelope.TenantID, value)
		if err != nil {
			return err
		}
		for _, audience := range []struct {
			kind     string
			internal bool
		}{{kind: "PDF", internal: true}, {kind: "PDF_CUSTOMER", internal: false}} {
			var existing database.ReportArtifact
			if err := tx.Where("tenant_id=? AND snapshot_id=? AND kind=?", envelope.TenantID, snapshot.ID, audience.kind).First(&existing).Error; err == nil {
				continue
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			html, err := reportcore.HTMLForPDF(value, availability, audience.internal)
			if err != nil {
				return err
			}
			reader, err := deps.Renderer.Render(ctx, strings.NewReader(string(html)), assets)
			if err != nil {
				if messaging.HasAttempt(ctx) && messaging.Attempt(ctx) < 3 {
					return err
				}
				return createHTMLFallback(ctx, deps.Store, tx, envelope, snapshot, deps.Now().UTC())
			}
			data, readErr := io.ReadAll(io.LimitReader(reader, 64<<20+1))
			reader.Close()
			if readErr != nil {
				return readErr
			}
			if len(data) == 0 || len(data) > 64<<20 {
				return fmt.Errorf("report PDF exceeds limit")
			}
			if deps.Store.Client == nil {
				return createHTMLFallback(ctx, deps.Store, tx, envelope, snapshot, deps.Now().UTC())
			}
			key, digest, err := deps.Store.PutDerivative(ctx, envelope.TenantID, snapshot.ID, "report-"+strings.ToLower(audience.kind), "application/pdf", data)
			if err != nil {
				return err
			}
			now := deps.Now().UTC()
			artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: envelope.TenantID, SnapshotID: snapshot.ID, Kind: audience.kind, ObjectKey: key, SHA256: digest, Status: "READY", CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
				return err
			}
			if err := emitReady(tx, envelope, snapshot, artifact, now); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func loadDisplayAssets(ctx context.Context, tx *gorm.DB, store objectstore.Store, tenantID identity.ID, snapshot reportcore.Snapshot) (pdf.AssetBundle, map[string]bool, error) {
	assets, available := pdf.AssetBundle{}, make(map[string]bool, len(snapshot.Evidence))
	for _, evidence := range snapshot.Evidence {
		available[evidence.ID] = false
		if evidence.Availability == "MISSING" {
			continue
		}
		var derivative database.MediaDerivative
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind=?", tenantID, evidence.ID, "DISPLAY").First(&derivative).Error; err == gorm.ErrRecordNotFound {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		data, err := store.Read(ctx, derivative.ObjectKey)
		if err != nil {
			return nil, nil, err
		}
		assets["evidence-"+evidence.ID+".jpg"] = data
		available[evidence.ID] = true
	}
	return assets, available, nil
}

func createHTMLFallback(ctx context.Context, store objectstore.Store, tx *gorm.DB, envelope events.RawEnvelope, snapshot database.ReportSnapshot, now time.Time) error {
	var html string
	var value reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &value); err == nil {
		if rendered, renderErr := reportcore.HTMLForPDF(value, map[string]bool{}, true); renderErr == nil {
			html = string(rendered)
		}
	}
	if html == "" {
		if err := json.Unmarshal(snapshot.HTML, &html); err != nil {
			html = string(snapshot.HTML)
		}
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
