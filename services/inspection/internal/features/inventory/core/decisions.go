package core

import (
	"context"
	"encoding/json"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Decide applies item decisions with optimistic locking and writes audit rows atomically.
func (s Service) Decide(ctx context.Context, in DecideInput) (DecideResult, error) {
	if len(in.Decisions) < 1 || len(in.Decisions) > MaxDecisions {
		return DecideResult{}, apperror.New(apperror.InvalidInput, "decisions", "between 1 and 200 decisions are required")
	}
	var out DecideResult
	err := s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		v, err := s.editable(tx, in.TenantID, in.OriginVersionID)
		if err != nil {
			return err
		}
		for _, d := range in.Decisions {
			var item database.InventoryItem
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, v.ID, d.ItemID).First(&item).Error; err != nil {
				return apperror.New(apperror.NotFound, "itemId", "inventory item not found")
			}
			if item.Version != d.ExpectedVersion {
				return apperror.New(apperror.Conflict, "expectedVersion", "item changed; refresh and retry")
			}
			if item.MergedIntoID != nil {
				return apperror.New(apperror.InvalidState, "itemId", "replaced inventory items cannot be decided")
			}
			var environment database.InventoryEnvironment
			if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, v.ID, item.EnvironmentID).First(&environment).Error; err != nil || environment.Decision == "REJECTED" {
				return apperror.New(apperror.InvalidState, "environmentId", "item environment is excluded")
			}
			before := marshal(item)
			switch d.Action {
			case "ACCEPT":
				if item.Decision == "ACCEPTED" || item.Decision == "EDITED" || item.Decision == "ADDED" {
					out.Items = append(out.Items, item)
					continue
				}
				if item.Decision == "REJECTED" || item.Decision == "PENDING" {
					if err := applyAccept(tx, in.TenantID, &item); err != nil {
						return err
					}
				}
			case "REJECT":
				if !validReason(d.Reason) {
					return apperror.New(apperror.InvalidInput, "reason", "invalid rejection reason")
				}
				if utf8Count(d.ReasonText) > 500 {
					return apperror.New(apperror.InvalidInput, "reasonText", "reason text exceeds 500 characters")
				}
				item.Decision = "REJECTED"
				item.RejectionReason = &d.Reason
				if d.ReasonText != "" {
					item.RejectionText = &d.ReasonText
				}
			case "EDIT":
				if d.Patch == nil {
					return apperror.New(apperror.InvalidInput, "patch", "patch is required")
				}
				p := *d.Patch
				if err := ValidateItemPatch(p); err != nil {
					return err
				}
				if (item.Decision == "PENDING" || item.Decision == "REJECTED") && !item.Declared {
					unavailable, err := itemEvidenceUnavailable(tx, in.TenantID, item.ID)
					if err != nil {
						return err
					}
					if unavailable {
						return apperror.New(apperror.InvalidState, "itemId", "EVIDENCE_UNAVAILABLE")
					}
				}
				if p.EnvironmentID != nil {
					var env database.InventoryEnvironment
					if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=? AND decision<>'REJECTED'", in.TenantID, v.ID, *p.EnvironmentID).First(&env).Error; err != nil {
						return apperror.New(apperror.InvalidInput, "environmentId", "environment must belong to this draft")
					}
					if env.ID != item.EnvironmentID {
						var count int64
						if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", in.TenantID, v.ID, env.ID).Count(&count).Error; err != nil {
							return err
						}
						if count >= MaxItemsPerEnvironment {
							return apperror.New(apperror.InvalidInput, "environmentId", "LIMIT_ITEMS_PER_ENVIRONMENT")
						}
					}
				}
				applyPatch(&item, p)
				item.Decision = "EDITED"
			default:
				return apperror.New(apperror.InvalidInput, "action", "unknown decision action")
			}
			now := s.now()
			item.Version++
			item.DecidedBy = &in.ActorMembershipID
			item.DecidedAt = &now
			item.UpdatedAt = now
			if err := tx.Save(&item).Error; err != nil {
				return err
			}
			reason := d.ReasonText
			if d.Action == "REJECT" {
				reason = d.Reason
				if d.ReasonText != "" {
					reason += ": " + d.ReasonText
				}
			}
			if err := s.audit(tx, in.TenantID, v.ID, "ITEM", item.ID, d.Action, before, marshal(item), reason, in.ActorMembershipID, now); err != nil {
				return err
			}
			out.Items = append(out.Items, item)
		}
		return s.emptyEnvironments(tx, in.TenantID, v.ID, &out)
	})
	return out, err
}

// BulkAccept accepts pending items meeting the confidence threshold.
func (s Service) BulkAccept(ctx context.Context, tenantID, versionID, environmentID, actor identity.ID, threshold int) (int, error) {
	if threshold < 0 || threshold > 10000 {
		return 0, apperror.New(apperror.InvalidInput, "minimumConfidenceBps", "threshold must be 0..10000")
	}
	accepted := 0
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if _, err := s.editable(tx, tenantID, versionID); err != nil {
			return err
		}
		var items []database.InventoryItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision='PENDING' AND confidence_bps>=?", tenantID, versionID, environmentID, threshold).Find(&items).Error; err != nil {
			return err
		}
		var environment database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=? AND decision<>'REJECTED'", tenantID, versionID, environmentID).First(&environment).Error; err != nil {
			return apperror.New(apperror.NotFound, "environmentId", "environment not found")
		}
		now := s.now()
		for _, item := range items {
			before := marshal(item)
			if err := applyAccept(tx, tenantID, &item); err != nil {
				code, _, safe := apperror.Public(err)
				if code == apperror.InvalidState && safe == "EVIDENCE_UNAVAILABLE" {
					continue
				}
				return err
			}
			item.Version++
			item.DecidedBy = &actor
			item.DecidedAt = &now
			item.UpdatedAt = now
			if err := tx.Save(&item).Error; err != nil {
				return err
			}
			if err := s.audit(tx, tenantID, versionID, "ITEM", item.ID, "ACCEPT", before, marshal(item), "", actor, now); err != nil {
				return err
			}
			accepted++
		}
		return nil
	})
	return accepted, err
}

func applyAccept(tx *gorm.DB, tenantID identity.ID, item *database.InventoryItem) error {
	if !item.Declared {
		unavailable, err := itemEvidenceUnavailable(tx, tenantID, item.ID)
		if err != nil {
			return err
		}
		if unavailable {
			return apperror.New(apperror.InvalidState, "itemId", "EVIDENCE_UNAVAILABLE")
		}
	}
	if len(item.Suggested) > 0 {
		var suggested struct {
			Condition string `json:"condition"`
		}
		if err := json.Unmarshal(item.Suggested, &suggested); err != nil {
			return apperror.New(apperror.InvalidState, "itemId", "suggested item data is invalid")
		}
		var edits int64
		if err := tx.Model(&database.InventoryDecision{}).Where("tenant_id=? AND target_type='ITEM' AND target_id=? AND action='EDIT'", item.TenantID, item.ID).Count(&edits).Error; err != nil {
			return err
		}
		if edits == 0 && suggested.Condition != "" {
			if !IsCondition(suggested.Condition) {
				return apperror.New(apperror.InvalidState, "itemId", "suggested condition is invalid")
			}
			item.Condition = suggested.Condition
		}
	}
	item.Decision = "ACCEPTED"
	item.RejectionReason = nil
	item.RejectionText = nil
	return nil
}

func (s Service) emptyEnvironments(tx *gorm.DB, tenant, version identity.ID, out *DecideResult) error {
	var envs []database.InventoryEnvironment
	if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenant, version).Find(&envs).Error; err != nil {
		return err
	}
	for _, env := range envs {
		var count int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND environment_id=? AND decision IN ('ACCEPTED','EDITED','ADDED') AND merged_into_id IS NULL", tenant, env.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			out.EmptyEnvironments = append(out.EmptyEnvironments, env.ID)
		}
	}
	return nil
}

func validReason(v string) bool {
	switch v {
	case "NOT_EXISTS", "NOT_RELEVANT", "DUPLICATE", "PERSONAL_OBJECT", "OTHER":
		return true
	}
	return false
}

func utf8Count(v string) int { return len([]rune(v)) }

func applyPatch(item *database.InventoryItem, p ItemPatch) {
	if p.EnvironmentID != nil {
		item.EnvironmentID = *p.EnvironmentID
	}
	if p.TypeCode != nil {
		item.TypeCode = *p.TypeCode
	}
	if p.Name != nil {
		item.Name = *p.Name
	}
	if p.Material != nil {
		if *p.Material == "" {
			item.Material = nil
		} else {
			item.Material = p.Material
		}
	}
	if p.Color != nil {
		if *p.Color == "" {
			item.Color = nil
		} else {
			item.Color = p.Color
		}
	}
	if p.Quantity != nil {
		item.Quantity = p.Quantity
	}
	if p.Condition != nil {
		item.Condition = *p.Condition
	}
	if p.Observation != nil {
		item.Observation = *p.Observation
	}
	if p.Limitation != nil {
		if *p.Limitation == "" {
			item.Limitation = nil
		} else {
			item.Limitation = p.Limitation
		}
	}
}

func itemEvidenceUnavailable(tx *gorm.DB, tenant, itemID identity.ID) (bool, error) {
	var evidence []database.InventoryItemEvidence
	if err := tx.Where("tenant_id=? AND item_id=?", tenant, itemID).Find(&evidence).Error; err != nil {
		return false, err
	}
	availability, err := LoadEvidenceAvailability(tx, tenant, evidence)
	if err != nil {
		return false, err
	}
	if len(evidence) == 0 {
		return false, nil
	}
	for _, row := range evidence {
		if availability[row.ID] != "UNAVAILABLE" {
			return false, nil
		}
	}
	return true, nil
}
