package core

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const MaxContacts = 10

type ContactInput struct{ Channel, Value string }
type ParticipantView struct {
	Participant database.Participant
	Contacts    []database.ParticipantContact
	Selected    []identity.ID
	Verified    map[identity.ID]bool
}

type Service struct {
	DB         *gorm.DB
	Authorizer auth.Authorizer
}

func (s Service) Upsert(ctx context.Context, tenantID, businessUnitID identity.ID, participantID *identity.ID, expectedVersion int64, name, role, idempotencyKey string, contacts []ContactInput) (ParticipantView, error) {
	if tenantID == (identity.ID{}) || businessUnitID == (identity.ID{}) || strings.TrimSpace(idempotencyKey) == "" {
		return ParticipantView{}, apperror.New(apperror.InvalidInput, "input", "tenant, business unit, and idempotency key are required")
	}
	name, role = strings.TrimSpace(name), strings.TrimSpace(role)
	if name == "" || role == "" {
		return ParticipantView{}, apperror.New(apperror.InvalidInput, "participant", "name and segment role are required")
	}
	if len([]rune(name)) > 200 || len(contacts) > MaxContacts {
		return ParticipantView{}, apperror.New(apperror.InvalidInput, "contacts", "participant limit exceeded")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: businessUnitID}, true); err != nil {
		return ParticipantView{}, err
	}
	if participantID != nil {
		var current database.Participant
		if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", tenantID, *participantID).First(&current).Error
		}); err != nil {
			return ParticipantView{}, apperror.New(apperror.NotFound, "participantId", "participant not found")
		}
		if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.BusinessUnitID}, true); err != nil {
			return ParticipantView{}, err
		}
	}
	normalized := make([]ContactInput, 0, len(contacts))
	for _, c := range contacts {
		n, err := Normalize(c.Channel, c.Value)
		if err != nil {
			return ParticipantView{}, apperror.New(apperror.InvalidInput, "contacts", err.Error())
		}
		normalized = append(normalized, ContactInput{Channel: strings.ToUpper(c.Channel), Value: n})
	}
	var result ParticipantView
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var participant database.Participant
		if participantID != nil {
			if err := tx.Where("tenant_id=? AND id=?", tenantID, *participantID).First(&participant).Error; err != nil {
				return apperror.New(apperror.NotFound, "participantId", "participant not found")
			}
			if participant.IdempotencyKey == idempotencyKey {
				result.Participant = participant
				return s.load(tx, &result)
			}
			if participant.Version != expectedVersion {
				return apperror.New(apperror.Conflict, "version", "stale participant version")
			}
			now := time.Now().UTC()
			updated := tx.Model(&database.Participant{}).Where("tenant_id=? AND id=? AND version=?", tenantID, *participantID, expectedVersion).Updates(map[string]any{"business_unit_id": businessUnitID, "name": name, "segment_role": role, "idempotency_key": idempotencyKey, "version": expectedVersion + 1, "updated_at": now})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return apperror.New(apperror.Conflict, "version", "stale participant version")
			}
			participant.BusinessUnitID, participant.Name, participant.SegmentRole, participant.IdempotencyKey = businessUnitID, name, role, idempotencyKey
			participant.Version++
			participant.UpdatedAt = now
			for _, c := range normalized {
				contact := database.ParticipantContact{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participant.ID, Channel: c.Channel, Value: c.Value, Normalized: c.Value, Active: true, CreatedAt: now, UpdatedAt: now}
				if err := tx.Where("tenant_id=? AND participant_id=? AND channel=? AND normalized=?", tenantID, participant.ID, c.Channel, c.Value).FirstOrCreate(&contact).Error; err != nil {
					return err
				}
			}
			result.Participant = participant
			return s.load(tx, &result)
		}
		err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&participant).Error
		if err == nil {
			result.Participant = participant
			return s.load(tx, &result)
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		now := time.Now().UTC()
		participant = database.Participant{ID: identity.NewID(), TenantID: tenantID, BusinessUnitID: businessUnitID, Name: name, SegmentRole: role, Status: "ACTIVE", Version: 1, IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&participant).Error; err != nil {
			return err
		}
		for _, c := range normalized {
			contact := database.ParticipantContact{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participant.ID, Channel: c.Channel, Value: c.Value, Normalized: c.Value, Active: true, CreatedAt: now, UpdatedAt: now}
			if err := tx.Where("tenant_id=? AND participant_id=? AND channel=? AND normalized=?", tenantID, participant.ID, c.Channel, c.Value).FirstOrCreate(&contact).Error; err != nil {
				return err
			}
		}
		result.Participant = participant
		return s.load(tx, &result)
	})
	return result, err
}

func (s Service) Verify(ctx context.Context, tenantID, contactID identity.ID, success bool, idempotencyKey string) (database.ContactVerification, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return database.ContactVerification{}, apperror.New(apperror.InvalidInput, "idempotencyKey", "idempotency key is required")
	}
	var unitID identity.ID
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var contact database.ParticipantContact
		if err := tx.Where("tenant_id=? AND id=?", tenantID, contactID).First(&contact).Error; err != nil {
			return err
		}
		var participant database.Participant
		if err := tx.Where("tenant_id=? AND id=?", tenantID, contact.ParticipantID).First(&participant).Error; err != nil {
			return err
		}
		unitID = participant.BusinessUnitID
		return nil
	}); err != nil {
		return database.ContactVerification{}, apperror.New(apperror.NotFound, "contactId", "contact not found")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: unitID}, true); err != nil {
		return database.ContactVerification{}, err
	}
	var verification database.ContactVerification
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND idempotency_key=?", tenantID, idempotencyKey).First(&verification).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var contact database.ParticipantContact
		if err := tx.Where("tenant_id=? AND id=?", tenantID, contactID).First(&contact).Error; err != nil {
			return apperror.New(apperror.NotFound, "contactId", "contact not found")
		}
		now := time.Now().UTC()
		verification = database.ContactVerification{ID: identity.NewID(), TenantID: tenantID, ContactID: contactID, IdempotencyKey: idempotencyKey, Status: "FAILED", CreatedAt: now}
		if success {
			verification.Status = "VERIFIED"
			verification.VerifiedAt = &now
		}
		return tx.Create(&verification).Error
	})
	return verification, err
}

func (s Service) SetChannels(ctx context.Context, tenantID, participantID identity.ID, contactIDs []identity.ID, expectedVersion int64) (ParticipantView, error) {
	if len(contactIDs) == 0 {
		return ParticipantView{}, apperror.New(apperror.InvalidInput, "contactIds", "at least one verified channel is required")
	}
	var current database.Participant
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", tenantID, participantID).First(&current).Error
	}); err != nil {
		return ParticipantView{}, apperror.New(apperror.NotFound, "participantId", "participant not found")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.BusinessUnitID}, true); err != nil {
		return ParticipantView{}, err
	}
	var result ParticipantView
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var p database.Participant
		if err := tx.Where("tenant_id=? AND id=?", tenantID, participantID).First(&p).Error; err != nil {
			return apperror.New(apperror.NotFound, "participantId", "participant not found")
		}
		if p.Status != "ACTIVE" {
			return apperror.New(apperror.InvalidState, "participantId", "participant is inactive")
		}
		unique := map[identity.ID]struct{}{}
		for _, id := range contactIDs {
			unique[id] = struct{}{}
		}
		result.Participant = p
		if err := s.load(tx, &result); err != nil {
			return err
		}
		if sameIDs(result.Selected, unique) {
			return nil
		}
		if p.Version != expectedVersion {
			return apperror.New(apperror.Conflict, "version", "stale participant version")
		}
		for id := range unique {
			var count int64
			err := tx.Table("participants.contacts c").Joins("JOIN participants.contact_verifications v ON v.contact_id=c.id AND v.tenant_id=c.tenant_id").Where("c.tenant_id=? AND c.participant_id=? AND c.id=? AND c.active=true AND v.status='VERIFIED'", tenantID, participantID, id).Count(&count).Error
			if err != nil {
				return err
			}
			if count == 0 {
				return apperror.New(apperror.InvalidState, "contactIds", "selected channel must be verified and active")
			}
		}
		if err := tx.Where("tenant_id=? AND participant_id=?", tenantID, participantID).Delete(&database.ChannelSelection{}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for id := range unique {
			if err := tx.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, ContactID: id, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		updated := tx.Model(&database.Participant{}).Where("tenant_id=? AND id=? AND version=?", tenantID, participantID, expectedVersion).Updates(map[string]any{"version": expectedVersion + 1, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale participant version")
		}
		p.Version++
		result.Participant = p
		return s.load(tx, &result)
	})
	return result, err
}

func sameIDs(current []identity.ID, wanted map[identity.ID]struct{}) bool {
	if len(current) != len(wanted) {
		return false
	}
	for _, id := range current {
		if _, ok := wanted[id]; !ok {
			return false
		}
	}
	return true
}

func (s Service) Deactivate(ctx context.Context, tenantID, participantID identity.ID, expectedVersion int64) error {
	var current database.Participant
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", tenantID, participantID).First(&current).Error
	}); err != nil {
		return apperror.New(apperror.NotFound, "participantId", "participant not found")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.BusinessUnitID}, true); err != nil {
		return err
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Participant{}).Where("tenant_id=? AND id=? AND version=? AND status='ACTIVE'", tenantID, participantID, expectedVersion).Updates(map[string]any{"status": "INACTIVE", "version": expectedVersion + 1, "updated_at": time.Now().UTC()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "participant not active at expected version")
		}
		return nil
	})
}

func (s Service) Get(ctx context.Context, tenantID, id identity.ID) (ParticipantView, error) {
	var v ParticipantView
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, id).First(&v.Participant).Error; err != nil {
			return apperror.New(apperror.NotFound, "id", "participant not found")
		}
		return s.load(tx, &v)
	})
	if err != nil {
		return ParticipantView{}, err
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: v.Participant.BusinessUnitID}, false); err != nil {
		return ParticipantView{}, err
	}
	return v, nil
}

func (s Service) List(ctx context.Context, tenantID identity.ID, search string, first int, after string) ([]ParticipantView, string, bool, error) {
	principal, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.ParticipationAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false)
	if err != nil {
		return nil, "", false, err
	}
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return nil, "", false, apperror.New(apperror.InvalidInput, "first", "page size exceeds 100")
	}
	allowed := map[identity.ID]struct{}{}
	if !hasRole(principal.Roles, auth.TenantAdmin) {
		for _, scope := range principal.Scopes {
			if scope.Kind == "BUSINESS_UNIT" {
				allowed[scope.ID] = struct{}{}
			}
		}
		if len(allowed) == 0 {
			return []ParticipantView{}, "", false, nil
		}
	}
	var rows []database.Participant
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID)
		if len(allowed) > 0 {
			ids := make([]identity.ID, 0, len(allowed))
			for id := range allowed {
				ids = append(ids, id)
			}
			q = q.Where("business_unit_id IN ?", ids)
		}
		if search != "" {
			q = q.Where("name ILIKE ?", "%"+strings.ReplaceAll(search, "%", "\\%")+"%")
		}
		if after != "" {
			q = q.Where("id > ?", after)
		}
		return q.Order("id").Limit(first + 1).Find(&rows).Error
	})
	if err != nil {
		return nil, "", false, err
	}
	has := len(rows) > first
	if has {
		rows = rows[:first]
	}
	out := make([]ParticipantView, len(rows))
	for i, p := range rows {
		out[i].Participant = p
	}
	// The list contract includes delivery channels and their verification
	// state. Load those tenant-scoped children before returning a page; callers
	// must never infer that an omitted channel is unverified.
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		for i := range out {
			if err := s.load(tx, &out[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, "", false, err
	}
	end := ""
	if len(rows) > 0 {
		end = rows[len(rows)-1].ID.String()
	}
	return out, end, has, nil
}

func hasRole(roles []string, wanted string) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func (s Service) load(tx *gorm.DB, v *ParticipantView) error {
	if err := tx.Where("tenant_id=? AND participant_id=?", v.Participant.TenantID, v.Participant.ID).Order("created_at").Find(&v.Contacts).Error; err != nil {
		return err
	}
	var selections []database.ChannelSelection
	if err := tx.Where("tenant_id=? AND participant_id=?", v.Participant.TenantID, v.Participant.ID).Find(&selections).Error; err != nil {
		return err
	}
	for _, x := range selections {
		v.Selected = append(v.Selected, x.ContactID)
	}
	v.Verified = make(map[identity.ID]bool)
	var verified []identity.ID
	if err := tx.Model(&database.ContactVerification{}).Where("tenant_id=? AND contact_id IN ? AND status='VERIFIED'", v.Participant.TenantID, contactIDs(v.Contacts)).Distinct("contact_id").Pluck("contact_id", &verified).Error; err != nil {
		return err
	}
	for _, id := range verified {
		v.Verified[id] = true
	}
	sort.Slice(v.Selected, func(i, j int) bool { return v.Selected[i].String() < v.Selected[j].String() })
	return nil
}

func contactIDs(contacts []database.ParticipantContact) []identity.ID {
	ids := make([]identity.ID, 0, len(contacts))
	for _, contact := range contacts {
		ids = append(ids, contact.ID)
	}
	return ids
}

var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func Normalize(channel, value string) (string, error) {
	channel = strings.ToUpper(strings.TrimSpace(channel))
	value = strings.TrimSpace(value)
	switch channel {
	case "EMAIL":
		address, err := mail.ParseAddress(value)
		if err != nil || address.Address != value {
			return "", fmt.Errorf("invalid email")
		}
		return strings.ToLower(value), nil
	case "SMS", "WHATSAPP":
		value = strings.ReplaceAll(value, " ", "")
		value = strings.ReplaceAll(value, "-", "")
		value = strings.ReplaceAll(value, "(", "")
		value = strings.ReplaceAll(value, ")", "")
		if !phonePattern.MatchString(value) {
			return "", fmt.Errorf("invalid E.164 phone")
		}
		return value, nil
	default:
		return "", fmt.Errorf("unknown channel")
	}
}
