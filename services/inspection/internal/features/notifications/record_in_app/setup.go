// Package record_in_app projects durable business events into the recipient's
// private in-app notification history.
package record_in_app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type notice struct {
	kind, title, body string
	inspectionID      identity.ID
	eventKey          identity.ID
}

// Setup returns an idempotent event handler for in-app notifications.
func Setup() func(context.Context, *gorm.DB, events.RawEnvelope) error {
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var value notice
		switch envelope.Type {
		case "inspection.created.v1":
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			value = notice{kind: "INSPECTION_CREATED", title: "Nova vistoria criada", body: "Uma vistoria foi adicionada à sua operação.", inspectionID: payload.InspectionID}
		case "recapture.requested.v1":
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			value = notice{kind: "RECAPTURE_REQUESTED", title: "Novas fotos solicitadas", body: "A vistoria precisa de fotos adicionais para continuar.", inspectionID: payload.InspectionID}
		case "recapture.completed.v1":
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
				Corrected    bool        `json:"corrected"`
				Expired      bool        `json:"expired"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			if payload.Expired {
				value = notice{kind: "RECAPTURE_EXPIRED", title: "Prazo de fotos complementares vencido", body: "O prazo terminou sem o envio das fotos solicitadas.", inspectionID: payload.InspectionID}
			} else if payload.Corrected {
				value = notice{kind: "RECAPTURE_COMPLETED", title: "Fotos complementares recebidas", body: "As novas fotos foram recebidas e a vistoria pode continuar.", inspectionID: payload.InspectionID}
			} else {
				return nil
			}
		case "report.ready.v1":
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
				SnapshotID   identity.ID `json:"snapshotId"`
				Kind         string      `json:"kind"`
				Status       string      `json:"status"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.SnapshotID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			if payload.Status != "READY" || (payload.Kind != "PDF" && payload.Kind != "PDF_CUSTOMER") {
				return nil
			}
			kind, title := "REPORT_READY", "Laudo disponível"
			if payload.Kind == "PDF_CUSTOMER" {
				kind, title = "REPORT_PUBLISHED", "Laudo publicado"
			}
			value = notice{kind: kind, title: title, body: "O laudo da vistoria está pronto para consulta.", inspectionID: payload.InspectionID, eventKey: payload.SnapshotID}
			if payload.SnapshotID != (identity.ID{}) {
				value.body += " Versão " + payload.SnapshotID.String()[:8] + "."
			}
		default:
			return messaging.ErrPermanent
		}
		return record(ctx, tx, envelope, value)
	}
}

func record(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope, value notice) error {
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, value.inspectionID).First(&inspection).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var asset database.Asset
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, inspection.AssetID).First(&asset).Error; err != nil {
		return err
	}
	project := ""
	if inspection.ProjectID != nil {
		var row database.Project
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, *inspection.ProjectID).First(&row).Error; err == nil {
			project = " (projeto " + row.ID.String()[:8] + ")"
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if value.kind == "REPORT_PUBLISHED" {
		var publication database.ReportPublication
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND snapshot_id=? AND status='PUBLISHED'", envelope.TenantID, inspection.ID, value.eventKey).Take(&publication).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
	}
	body := strings.TrimSpace(value.body + " Imóvel: " + asset.Name + ". " + asset.Address + project + ".")
	if value.kind == "RECAPTURE_REQUESTED" {
		var request database.RecaptureRequest
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND status IN ?", envelope.TenantID, inspection.ID, []string{"REQUESTED", "ACCESSED"}).Order("created_at DESC").First(&request).Error; err == nil {
			body += " Prazo: " + request.DeadlineAt.UTC().Format("02/01/2006 15:04 UTC") + "."
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	var managers []database.Membership
	if value.kind == "REPORT_PUBLISHED" {
		query := tx.WithContext(ctx).Table("access.memberships AS m").Select("m.*").Joins("JOIN access.resource_scopes AS s ON s.tenant_id=m.tenant_id AND s.membership_id=m.id").Where("m.tenant_id=? AND m.status='ACTIVE' AND m.role='CUSTOMER_VIEWER'", envelope.TenantID).Where("(s.kind='BUSINESS_UNIT' AND s.resource_id=?) OR (s.kind='ASSET' AND s.resource_id=?) OR (s.kind='INSPECTION' AND s.resource_id=?)", inspection.BusinessUnitID, inspection.AssetID, inspection.ID)
		if inspection.ProjectID != nil {
			query = query.Or("m.tenant_id=? AND m.status='ACTIVE' AND m.role='CUSTOMER_VIEWER' AND EXISTS (SELECT 1 FROM access.resource_scopes s WHERE s.tenant_id=m.tenant_id AND s.membership_id=m.id AND s.kind='PROJECT' AND s.resource_id=?)", envelope.TenantID, *inspection.ProjectID)
		}
		if err := query.Distinct("m.id").Find(&managers).Error; err != nil {
			return err
		}
	} else if err := tx.WithContext(ctx).Where("tenant_id=? AND status='ACTIVE' AND role='TENANT_ADMIN'", envelope.TenantID).Find(&managers).Error; err != nil {
		return err
	}
	var scoped []database.Membership
	query := tx.WithContext(ctx).Table("access.memberships AS m").Select("m.*").Joins("JOIN access.resource_scopes AS s ON s.tenant_id=m.tenant_id AND s.membership_id=m.id").Where("m.tenant_id=? AND m.status='ACTIVE' AND m.role=?", envelope.TenantID, "MANAGER").Where(
		"(s.kind='BUSINESS_UNIT' AND s.resource_id=?) OR (s.kind='ASSET' AND s.resource_id=?) OR (s.kind='INSPECTION' AND s.resource_id=?)",
		inspection.BusinessUnitID, inspection.AssetID, inspection.ID,
	)
	if inspection.ProjectID != nil {
		query = query.Or("m.tenant_id=? AND m.status='ACTIVE' AND m.role=? AND EXISTS (SELECT 1 FROM access.resource_scopes s WHERE s.tenant_id=m.tenant_id AND s.membership_id=m.id AND s.kind='PROJECT' AND s.resource_id=?)", envelope.TenantID, "MANAGER", *inspection.ProjectID)
	}
	if value.kind != "REPORT_PUBLISHED" {
		if err := query.Distinct("m.id").Find(&scoped).Error; err != nil {
			return err
		}
	}
	seen := make(map[identity.ID]struct{}, len(managers)+len(scoped))
	recipients := append(managers, scoped...)
	for _, recipient := range recipients {
		if _, duplicate := seen[recipient.ID]; duplicate {
			continue
		}
		seen[recipient.ID] = struct{}{}
		logicalEventID := envelope.ID
		if envelope.Type == "report.ready.v1" {
			logicalEventID = identity.NewDeterministicID("inspection/report-ready-notification", value.eventKey.String())
		}
		resourceKind := "INSPECTION"
		if value.kind == "REPORT_READY" {
			resourceKind = "REPORT"
		}
		row := database.RecipientNotification{ID: identity.NewDeterministicID("inspection/recipient-notification", recipient.ID.String()+":"+logicalEventID.String()+":"+value.kind), TenantID: envelope.TenantID, RecipientMembershipID: recipient.ID, EventID: logicalEventID, Kind: value.kind, Title: value.title, Body: body, ResourceKind: resourceKind, ResourceID: &value.inspectionID, CreatedAt: envelope.OccurredAt.UTC()}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("record in-app notification: %w", err)
		}
	}
	return nil
}
