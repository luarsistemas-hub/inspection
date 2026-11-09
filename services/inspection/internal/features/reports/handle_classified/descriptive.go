package handle_classified

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

type pinnedReportScope struct {
	Rooms           []catalog.ExpectedRoom   `json:"rooms"`
	HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
}

func buildDescriptiveReport(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, evidence []reportcore.Evidence, findings []reportcore.Finding, promptDigest string, jobStatus string) (*reportcore.ChecklistSection, *reportcore.ProcessingState, *reportcore.ScopeSnapshot, *reportcore.HandoverSection, *reportcore.Provenance, error) {
	var pinned database.InspectionScopeSnapshot
	err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&pinned).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, nil, nil, nil, err
	}
	if err == gorm.ErrRecordNotFound {
		// Legacy and video inspections have no pinned descriptive scope. Keep
		// their historical v1/v2 report shape exactly as it was.
		return nil, nil, nil, nil, nil, nil
	}
	checklist := &reportcore.ChecklistSection{Rooms: []reportcore.ChecklistRoom{}}
	scope := &reportcore.ScopeSnapshot{Digest: pinned.Digest, Version: pinned.Version, Profile: pinned.Profile, Document: pinned.Document}
	var doc pinnedReportScope
	{
		if decodeErr := json.Unmarshal(pinned.Document, &doc); decodeErr != nil {
			slog.WarnContext(ctx, "report_scope_document_invalid", "tenantId", tenantID.String(), "inspectionId", inspectionID.String(), "error", decodeErr)
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: pinned scope document is invalid", messaging.ErrPermanent)
		}
		scope.Unknown = len(doc.Rooms) == 0
		// Only submitted drafts are facts of an immutable report: an open or
		// abandoned recapture must not contribute answers or declarations.
		var answers []database.ComponentAnswer
		if err := tx.WithContext(ctx).Table("capture.component_answers AS a").Select("a.*").Joins("JOIN capture.capture_drafts AS d ON d.tenant_id=a.tenant_id AND d.id=a.draft_id").Joins("JOIN inspections.responsibilities AS r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id").Where("a.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED'", tenantID, inspectionID).Order("a.updated_at ASC").Find(&answers).Error; err != nil {
			return nil, nil, nil, nil, nil, err
		}
		answerByKey := make(map[string][]database.ComponentAnswer)
		for _, answer := range answers {
			answerByKey[answer.RequirementKey+"\x00"+answer.ComponentKey] = append(answerByKey[answer.RequirementKey+"\x00"+answer.ComponentKey], answer)
		}
		var catalogItems []database.InventoryItem
		var catalogEvidence []database.InventoryItemEvidence
		var catalogAttributeSources []database.InventoryItemAttributeSource
		catalogEnvironments := make(map[identity.ID]database.InventoryEnvironment)
		var catalogVersion database.OriginVersion
		catalogVersionErr := tx.WithContext(ctx).Where("tenant_id=? AND source_inspection_id=? AND source='CAPTURE' AND has_inventory", tenantID, inspectionID).Order("version_number DESC, created_at DESC, id DESC").First(&catalogVersion).Error
		if catalogVersionErr != nil && catalogVersionErr != gorm.ErrRecordNotFound {
			return nil, nil, nil, nil, nil, catalogVersionErr
		}
		if catalogVersionErr == nil {
			if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND component_key IS NOT NULL AND decision<>'REJECTED'", tenantID, catalogVersion.ID).Find(&catalogItems).Error; err != nil {
				return nil, nil, nil, nil, nil, err
			}
			if len(catalogItems) > 0 {
				ids := make([]identity.ID, 0, len(catalogItems))
				environmentIDs := make([]identity.ID, 0, len(catalogItems))
				for _, item := range catalogItems {
					ids = append(ids, item.ID)
					environmentIDs = append(environmentIDs, item.EnvironmentID)
				}
				var environments []database.InventoryEnvironment
				if err := tx.WithContext(ctx).Where("tenant_id=? AND id IN ?", tenantID, environmentIDs).Find(&environments).Error; err != nil {
					return nil, nil, nil, nil, nil, err
				}
				for _, environment := range environments {
					catalogEnvironments[environment.ID] = environment
				}
				if err := tx.WithContext(ctx).Where("tenant_id=? AND item_id IN ?", tenantID, ids).Order("position ASC").Find(&catalogEvidence).Error; err != nil {
					return nil, nil, nil, nil, nil, err
				}
				if err := tx.WithContext(ctx).Where("tenant_id=? AND item_id IN ?", tenantID, ids).Order("attribute_name ASC, source_kind ASC").Find(&catalogAttributeSources).Error; err != nil {
					return nil, nil, nil, nil, nil, err
				}
			}
		}
		for _, room := range doc.Rooms {
			outRoom := reportcore.ChecklistRoom{Key: room.Key, TypeCode: room.TypeCode, Name: room.Name, Components: []reportcore.ChecklistComponent{}}
			for _, component := range room.Components {
				key := "env:" + room.Key + "\x00" + component.Key
				answers := answerByKey[key]
				state := "NOT_OBSERVED"
				var evidenceIDs []string
				var reason string
				for _, answer := range answers {
					if answer.State == "OBSERVED" {
						state = "OBSERVED"
					} else if state != "OBSERVED" && answer.State == "PARTIAL" {
						state = "PARTIAL"
					} else if state == "NOT_OBSERVED" && answer.State == "NOT_APPLICABLE" {
						state = "NOT_APPLICABLE"
					} else if state == "NOT_OBSERVED" && answer.State == "IMPOSSIBLE" {
						state = "IMPOSSIBLE"
					}
					// The justification belongs to the answer that set the
					// component state, never to an answer of another state.
					if answer.State == state {
						reason = answer.Reason
					}
					var ids []identity.ID
					if decodeErr := json.Unmarshal(answer.EvidenceIDs, &ids); decodeErr != nil {
						slog.WarnContext(ctx, "report_component_evidence_ids_invalid", "tenantId", tenantID.String(), "inspectionId", inspectionID.String(), "answerId", answer.ID.String(), "error", decodeErr)
						return nil, nil, nil, nil, nil, fmt.Errorf("%w: component evidence references are invalid", messaging.ErrPermanent)
					}
					for _, id := range ids {
						if reportEvidenceExists(evidence, id.String()) {
							evidenceIDs = appendUnique(evidenceIDs, id.String())
						}
					}
				}
				if len(component.Views) > 0 {
					var missingViews []string
					state, missingViews = assessExpectedViews(state, component.Views, answers)
					if len(missingViews) > 0 {
						labels := make([]string, 0, len(missingViews))
						for _, view := range missingViews {
							labels = append(labels, expectedViewLabel(view))
						}
						reason = "Vistas pendentes: " + strings.Join(labels, ", ")
					}
				}
				entry := reportcore.ChecklistComponent{Key: component.Key, TypeCode: component.TypeCode, Label: component.Label, Status: state, ReviewStatus: "NOT_REVIEWED", EvidenceIDs: evidenceIDs}
				matches := matchCatalogItemsForRoom(catalogItems, catalogEnvironments, room.Key, component.Key)
				if len(matches) > 1 {
					entry.Conflict = true
					entry.Limitations = append(entry.Limitations, reportcore.LimitationAmbiguousCatalog)
				} else if len(matches) == 1 {
					item := matches[0]
					entry.TypeCode, entry.Description, entry.Condition, entry.AssessedScope = item.TypeCode, item.Description, item.Condition, item.AssessedScope
					var voiceConflict bool
					entry.AttributeSources, voiceConflict = reportAttributeSources(item.ID, catalogEvidence, catalogAttributeSources, evidence)
					entry.Conflict = entry.Conflict || voiceConflict
					entry.ReviewStatus = item.Decision
					if entry.ReviewStatus == "PENDING" {
						entry.ReviewStatus = "NOT_REVIEWED"
					}
					for _, link := range catalogEvidence {
						if link.ItemID == item.ID && reportEvidenceExists(evidence, link.MediaID.String()) {
							entry.EvidenceIDs = appendUnique(entry.EvidenceIDs, link.MediaID.String())
						}
					}
					for _, finding := range findings {
						for _, id := range finding.EvidenceIDs {
							if containsReportValue(entry.EvidenceIDs, id) {
								entry.Conflict = true
							}
						}
					}
				}
				if entry.Conflict && len(entry.Limitations) == 0 && !hasVoiceVisualConflict(entry.AttributeSources) {
					entry.Limitations = append(entry.Limitations, reportcore.LimitationCatalogFindingConflict)
				}
				if hasVoiceVisualConflict(entry.AttributeSources) {
					entry.Limitations = appendUnique(entry.Limitations, reportcore.LimitationSourceConflict)
				}
				if state == "OBSERVED" {
					entry.Status, reason = assessEvidenceState(state, entry.EvidenceIDs, evidence)
				}
				if entry.Status != state {
					entry.Limitations = appendUnique(entry.Limitations, reason)
				}
				if state == "NOT_APPLICABLE" || state == "IMPOSSIBLE" || (state == "PARTIAL" && strings.HasPrefix(reason, "Vistas pendentes:")) {
					if reason != "" {
						entry.Limitations = []string{reason}
					}
				}
				outRoom.Components = append(outRoom.Components, entry)
				checklist.Coverage.Expected++
				switch entry.Status {
				case "OBSERVED":
					checklist.Coverage.FullyAssessed++
				case "PARTIAL":
					checklist.Coverage.PartiallyAssessed++
				case "NOT_APPLICABLE":
					checklist.Coverage.NotApplicable++
				case "IMPOSSIBLE":
					checklist.Coverage.Impossible++
				default:
					checklist.Coverage.NotObserved++
				}
			}
			checklist.Rooms = append(checklist.Rooms, outRoom)
		}
		if len(doc.Rooms) == 0 {
			checklist.Coverage.UnknownScope = true
		}
		checklist.Coverage.Expected -= checklist.Coverage.NotApplicable
	}

	// Declarations live in the draft where they were made; a RECAPTURE draft
	// starts empty. Read every SUBMITTED draft of the inspection (the original
	// capture and each submitted recapture) and keep, per record key, the most
	// recent declaration. A draft still OPEN contributes nothing.
	var handoverRows []database.HandoverRecord
	if err := tx.WithContext(ctx).Table("capture.handover_records AS h").Select("h.*").Joins("JOIN capture.capture_drafts AS d ON d.tenant_id=h.tenant_id AND d.id=h.draft_id").Joins("JOIN inspections.responsibilities AS r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id").Where("h.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED'", tenantID, inspectionID).Order("h.updated_at DESC, h.version DESC, d.updated_at DESC, h.id DESC").Find(&handoverRows).Error; err != nil {
		return nil, nil, nil, nil, nil, err
	}
	handover := &reportcore.HandoverSection{Entries: []reportcore.HandoverEntry{}}
	seen := map[string]bool{}
	for _, row := range handoverRows {
		if seen[row.RecordKey] {
			continue
		}
		seen[row.RecordKey] = true
		var ids []identity.ID
		if decodeErr := json.Unmarshal(row.MediaIDs, &ids); decodeErr != nil {
			slog.WarnContext(ctx, "report_handover_media_ids_invalid", "tenantId", tenantID.String(), "inspectionId", inspectionID.String(), "recordKey", row.RecordKey, "error", decodeErr)
			return nil, nil, nil, nil, nil, fmt.Errorf("%w: handover evidence references are invalid", messaging.ErrPermanent)
		}
		entry := reportcore.HandoverEntry{Key: row.RecordKey, Kind: row.Kind, Status: "DECLARED", Payload: row.Payload, Declarant: row.Declarant}
		for _, id := range ids {
			if reportEvidenceExists(evidence, id.String()) {
				entry.EvidenceIDs = appendUnique(entry.EvidenceIDs, id.String())
			}
		}
		handover.Entries = append(handover.Entries, entry)
	}
	// Declared entries keep the stable record-key order of the report.
	sort.SliceStable(handover.Entries, func(i, j int) bool { return handover.Entries[i].Key < handover.Entries[j].Key })
	for _, required := range doc.HandoverRecords {
		if required.Required && !seen[required.Key] {
			handover.Entries = append(handover.Entries, reportcore.HandoverEntry{Key: required.Key, Kind: required.Kind, Status: "PENDING"})
		}
	}
	status := jobStatus
	if status == "" {
		status = "COMPLETED"
	}
	processing := &reportcore.ProcessingState{Status: status}
	if status == "FAILED" {
		processing.Message = "A análise não foi concluída."
	} else if status == "INCONCLUSIVE" {
		processing.Message = "A análise foi inconclusiva."
	}
	prov := &reportcore.Provenance{PromptRevision: promptDigest, ScopeDigest: scope.Digest}
	for _, item := range evidence {
		prov.EvidenceIDs = append(prov.EvidenceIDs, item.ID)
	}
	if len(findings) > 0 {
		prov.AnalysisMode = "FINDINGS"
	}
	return checklist, processing, scope, handover, prov, nil
}

func expectedViewLabel(value string) string {
	switch strings.ToLower(value) {
	case "wide":
		return "visão geral"
	case "detail":
		return "detalhe"
	case "left":
		return "lado esquerdo"
	case "right":
		return "lado direito"
	case "top":
		return "parte superior"
	case "bottom":
		return "parte inferior"
	default:
		return "vista complementar"
	}
}

func hasAvailableEvidence(ids []string, evidence []reportcore.Evidence) bool {
	for _, id := range ids {
		for _, item := range evidence {
			if item.ID == id && reportcore.EvidenceAvailable(item.Availability) {
				return true
			}
		}
	}
	return false
}

func assessEvidenceState(state string, ids []string, evidence []reportcore.Evidence) (string, string) {
	if state != "OBSERVED" || hasAvailableEvidence(ids, evidence) {
		return state, ""
	}
	if len(ids) == 0 {
		return "NOT_OBSERVED", "Nenhuma evidência disponível para avaliar o componente."
	}
	return "PARTIAL", "As evidências registradas estão indisponíveis; a avaliação visual ficou limitada."
}

func reportAttributeSources(itemID identity.ID, evidenceRows []database.InventoryItemEvidence, sources []database.InventoryItemAttributeSource, reportEvidence []reportcore.Evidence) ([]reportcore.AttributeSource, bool) {
	mediaByEvidence := make(map[identity.ID]string, len(evidenceRows))
	for _, row := range evidenceRows {
		if reportEvidenceExists(reportEvidence, row.MediaID.String()) {
			mediaByEvidence[row.ID] = row.MediaID.String()
		}
	}
	result := make([]reportcore.AttributeSource, 0)
	indexes := make(map[string]int)
	for _, source := range sources {
		if source.ItemID != itemID {
			continue
		}
		key := source.AttributeName + "\x00" + source.SourceKind
		index, exists := indexes[key]
		if !exists {
			index = len(result)
			indexes[key] = index
			result = append(result, reportcore.AttributeSource{Attribute: source.AttributeName, Source: source.SourceKind})
		}
		if source.VisualEvidenceID != nil {
			if mediaID := mediaByEvidence[*source.VisualEvidenceID]; mediaID != "" {
				result[index].EvidenceIDs = appendUnique(result[index].EvidenceIDs, mediaID)
			}
		}
	}
	return result, hasVoiceVisualConflict(result)
}

func hasVoiceVisualConflict(sources []reportcore.AttributeSource) bool {
	for _, source := range sources {
		if source.Source == "VOICE" && hasAttributeSource(sources, source.Attribute, "VISUAL") {
			return true
		}
	}
	return false
}

func hasAttributeSource(sources []reportcore.AttributeSource, attribute, sourceKind string) bool {
	for _, source := range sources {
		if source.Attribute == attribute && source.Source == sourceKind {
			return true
		}
	}
	return false
}

func assessExpectedViews(state string, expected []string, answers []database.ComponentAnswer) (string, []string) {
	if state != "OBSERVED" {
		return state, nil
	}
	observed := make(map[string]bool, len(answers))
	for _, answer := range answers {
		if answer.State == "OBSERVED" {
			observed[answer.ViewKey] = true
		}
	}
	missing := make([]string, 0)
	for _, view := range expected {
		if !observed[view] {
			missing = append(missing, view)
		}
	}
	if len(missing) > 0 {
		return "PARTIAL", missing
	}
	return state, missing
}

func matchCatalogItemsForRoom(items []database.InventoryItem, environments map[identity.ID]database.InventoryEnvironment, roomKey, componentKey string) []database.InventoryItem {
	roomIdentity := strings.TrimPrefix(roomKey, "new:")
	matches := make([]database.InventoryItem, 0, 1)
	for _, item := range items {
		if item.ComponentKey == nil || *item.ComponentKey != componentKey {
			continue
		}
		environment, ok := environments[item.EnvironmentID]
		if !ok || environment.IdentityID.String() != roomIdentity {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
func containsReportValue(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}
func reportEvidenceExists(evidence []reportcore.Evidence, id string) bool {
	for _, item := range evidence {
		if item.ID == id {
			return true
		}
	}
	return false
}

// includeSupplementalMedia resolves component-answer and handover media through
// the tenant-scoped media records. Unreceived and failed media remain explicitly
// unavailable and cannot become usable evidence in a report.
func includeSupplementalMedia(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, evidence []reportcore.Evidence) ([]reportcore.Evidence, error) {
	var scopeCount int64
	if err := tx.WithContext(ctx).Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Count(&scopeCount).Error; err != nil {
		return nil, err
	}
	if scopeCount == 0 {
		return evidence, nil
	}
	var rows []database.MediaObject
	// Only well-formed UUID arrays are expanded here. A malformed reference
	// list must not fail this query (which would be retried forever); the
	// descriptive builder decodes the same column and rejects it permanently.
	query := `SELECT DISTINCT m.* FROM media.media_objects m
		WHERE m.tenant_id = ? AND m.id IN (
			SELECT e.value::uuid
			FROM capture.component_answers a
			JOIN capture.capture_drafts d ON d.tenant_id=a.tenant_id AND d.id=a.draft_id
			JOIN inspections.responsibilities r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id
			CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(a.evidence_ids)='array' THEN a.evidence_ids ELSE '[]'::jsonb END) AS e(value)
			WHERE a.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED' AND e.value ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			UNION
			SELECT e.value::uuid
			FROM capture.handover_records h
			JOIN capture.capture_drafts d ON d.tenant_id=h.tenant_id AND d.id=h.draft_id
			JOIN inspections.responsibilities r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id
			CROSS JOIN LATERAL jsonb_array_elements_text(CASE WHEN jsonb_typeof(h.media_ids)='array' THEN h.media_ids ELSE '[]'::jsonb END) AS e(value)
			WHERE h.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED' AND e.value ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
		)`
	if err := tx.WithContext(ctx).Raw(query, tenantID, tenantID, inspectionID, tenantID, inspectionID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, media := range rows {
		if reportEvidenceExists(evidence, media.ID.String()) {
			continue
		}
		item := reportcore.Evidence{ID: media.ID.String(), RequirementKey: media.RequirementKey, Role: "CURRENT", Description: media.Description, CaptureSource: media.CaptureSource, Availability: "UNAVAILABLE"}
		if media.CapturedAt != nil {
			item.CapturedAt = media.CapturedAt.UTC().Format(time.RFC3339Nano)
		}
		if media.Status == "READY" || media.Status == "SCREENED" || media.Status == "VERIFIED" {
			var derivative database.MediaDerivative
			if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenantID, media.ID).Take(&derivative).Error; err == nil {
				item.DisplayDigest = derivative.SHA256
				item.Availability = "AVAILABLE"
			} else if err != gorm.ErrRecordNotFound {
				return nil, err
			}
		}
		evidence = append(evidence, item)
	}
	return evidence, nil
}
