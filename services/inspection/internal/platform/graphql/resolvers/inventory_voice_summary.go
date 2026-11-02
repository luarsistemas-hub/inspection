package resolvers

import (
	"context"
	"encoding/json"

	"inspection/libs/identity"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// hydrateInventoryVoiceProjection resolves stale citations and voice counts in
// a small number of tenant-scoped queries for the whole draft.
func hydrateInventoryVoiceProjection(ctx context.Context, db *gorm.DB, tenant identity.ID, inspectionID *identity.ID, draft *graphql1.InventoryDraft) {
	if draft == nil {
		return
	}
	draft.VoiceSummary = &graphql1.InventoryVoiceSummary{}
	for _, env := range draft.Environments {
		env.VoiceSummary = &graphql1.InventoryVoiceSummary{}
	}
	if db == nil {
		return
	}
	type citation struct {
		env    *graphql1.InventoryEnvironment
		item   *graphql1.InventoryItem
		source *graphql1.InventoryAttributeSource
	}
	citations := []citation{}
	noteIDs := []identity.ID{}
	pendingItems := map[*graphql1.InventoryEnvironment]map[string]bool{}
	for _, env := range draft.Environments {
		for _, item := range env.Items {
			item.VoiceConflicts = inventoryVoiceConflicts(item.Suggested)
			if item.Decision == "PENDING" && len(item.VoiceConflicts) > 0 {
				env.VoiceSummary.ConflictItems++
			}
			for _, s := range item.AttributeSources {
				if s.SourceKind == "VOICE" && s.NoteID != nil && s.RevisionID != nil {
					if item.Decision == "PENDING" {
						if pendingItems[env] == nil {
							pendingItems[env] = map[string]bool{}
						}
						pendingItems[env][item.ID] = true
					}
					nid, e := identity.ParseID(*s.NoteID)
					if e == nil {
						noteIDs = append(noteIDs, nid)
						citations = append(citations, citation{env, item, s})
					}
				}
			}
		}
	}
	for env, items := range pendingItems {
		env.VoiceSummary.PendingVoiceItems = len(items)
	}
	_ = (tenanttx.Runner{DB: db}).Within(ctx, tenant, func(tx *gorm.DB) error {
		type current struct {
			ID                identity.ID
			CurrentRevisionID *identity.ID
			InspectionID      identity.ID
			RequirementKey    string
			Status            string
			AnchorStatus      string
			NoSpeech          bool
		}
		byNote := map[identity.ID]current{}
		if len(noteIDs) > 0 {
			var rows []current
			if tx.WithContext(ctx).Table("transcription.audio_notes").Select("id,current_revision_id,inspection_id,requirement_key,status,anchor_status,no_speech").Where("tenant_id=? AND id IN ?", tenant, uniqueIDs(noteIDs)).Scan(&rows).Error == nil {
				for _, row := range rows {
					byNote[row.ID] = row
				}
			}
		}
		linked := map[identity.ID]bool{}
		staleItems := map[*graphql1.InventoryEnvironment]map[string]bool{}
		for _, c := range citations {
			nid, _ := identity.ParseID(*c.source.NoteID)
			row, ok := byNote[nid]
			if !ok {
				continue
			}
			linked[nid] = true
			if row.CurrentRevisionID != nil {
				v := row.CurrentRevisionID.String()
				c.source.CurrentRevisionID = &v
				c.source.Stale = *c.source.RevisionID != v
			}
			if c.item.Decision == "PENDING" {
				if c.source.Stale {
					if staleItems[c.env] == nil {
						staleItems[c.env] = map[string]bool{}
					}
					staleItems[c.env][c.item.ID] = true
				}
			}
		}
		for env, items := range staleItems {
			env.VoiceSummary.StaleSuggestions = len(items)
		}
		if inspectionID != nil {
			var rows []struct {
				ID             identity.ID
				RequirementKey string
			}
			if tx.WithContext(ctx).Table("transcription.audio_notes").Select("id,requirement_key").Where("tenant_id=? AND inspection_id=? AND status='COMPLETED' AND anchor_status='VALID' AND no_speech=false", tenant, *inspectionID).Scan(&rows).Error == nil {
				for _, note := range rows {
					if linked[note.ID] {
						continue
					}
					for _, env := range draft.Environments {
						if env.RequirementKey != nil && *env.RequirementKey == note.RequirementKey {
							env.VoiceSummary.NotesWithoutItem++
							break
						}
					}
				}
			}
		}
		for _, env := range draft.Environments {
			draft.VoiceSummary.PendingVoiceItems += env.VoiceSummary.PendingVoiceItems
			draft.VoiceSummary.ConflictItems += env.VoiceSummary.ConflictItems
			draft.VoiceSummary.StaleSuggestions += env.VoiceSummary.StaleSuggestions
			draft.VoiceSummary.NotesWithoutItem += env.VoiceSummary.NotesWithoutItem
		}
		return nil
	})
}

func inventoryVoiceConflicts(suggested map[string]any) []*graphql1.InventoryVoiceConflict {
	if suggested == nil {
		return []*graphql1.InventoryVoiceConflict{}
	}
	raw, ok := suggested["conflicts"]
	if !ok {
		return []*graphql1.InventoryVoiceConflict{}
	}
	data, _ := json.Marshal(raw)
	var values []struct {
		Attribute   string  `json:"attribute"`
		VisualValue string  `json:"visualValue"`
		VoiceValue  *string `json:"voiceValue"`
	}
	if json.Unmarshal(data, &values) != nil {
		return []*graphql1.InventoryVoiceConflict{}
	}
	out := make([]*graphql1.InventoryVoiceConflict, 0, len(values))
	for _, v := range values {
		if v.Attribute != "" && v.VisualValue != "" {
			out = append(out, &graphql1.InventoryVoiceConflict{AttributeName: v.Attribute, VisualValue: v.VisualValue, VoiceValue: v.VoiceValue})
		}
	}
	return out
}
