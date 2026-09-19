package delivery_status

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

type Service struct {
	DB  *gorm.DB
	Now func() time.Time
}

type Status struct {
	State, NextAction, OriginStatus, DeliveryStatus string
	RequestID, InspectionID                         *identity.ID
	ResponsibleEmail, DeliveryFailureCode           *string
	ResponsibilityStatus                            *string
	ResponsibilityVersion                           *int64
	CanCorrect                                      bool
	UpdatedAt                                       *time.Time
}

func (s Service) Load(ctx context.Context, tenantID, sessionID identity.ID) (Status, error) {
	if s.DB == nil {
		return Status{}, errors.New("onboarding delivery status: database unavailable")
	}
	var result Status
	err := s.DB.WithContext(ctx).Table("onboarding.requests r").
		Select("r.id AS request_id, r.inspection_id, r.status AS request_status").
		Where("r.tenant_id=? AND r.session_id=?", tenantID, sessionID).Scan(&result).Error
	if err != nil {
		return Status{}, err
	}
	if result.RequestID == nil {
		return result, nil
	}
	result.State = "SUBMITTED"
	result.OriginStatus = "NOT_REQUIRED"
	result.DeliveryStatus = "NOT_STARTED"
	if result.InspectionID == nil {
		return result, nil
	}
	var responsibility database.Responsibility
	if err := s.DB.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", tenantID, *result.InspectionID).First(&responsibility).Error; err != nil {
		return Status{}, err
	}
	status := responsibility.Status
	result.ResponsibilityStatus = &status
	version := responsibility.Version
	result.ResponsibilityVersion = &version
	var email string
	if err := s.DB.WithContext(ctx).Table("participants.contacts c").Select("c.value").Joins("JOIN participants.channel_selections s ON s.tenant_id=c.tenant_id AND s.contact_id=c.id").Where("c.tenant_id=? AND c.participant_id=? AND c.channel='EMAIL' AND c.active=true", tenantID, responsibility.ParticipantID).Order("c.updated_at desc").Limit(1).Scan(&email).Error; err != nil {
		return Status{}, err
	}
	if email != "" {
		result.ResponsibleEmail = &email
	}
	var delivery database.Delivery
	err = s.DB.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND logical_template='capture-link'", tenantID, *result.InspectionID).Order("created_at desc").First(&delivery).Error
	if err == nil {
		result.DeliveryStatus = delivery.Status
		result.UpdatedAt = &delivery.UpdatedAt
		if delivery.Status == "FAILED" || delivery.Status == "UNKNOWN" {
			var attempt database.ChannelAttempt
			if attemptErr := s.DB.WithContext(ctx).Where("tenant_id=? AND delivery_id=?", tenantID, delivery.ID).Order("updated_at desc").First(&attempt).Error; attemptErr == nil && attempt.LastError != "" {
				code := attempt.LastError
				result.DeliveryFailureCode = &code
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Status{}, err
	}
	result.CanCorrect = responsibility.Status == "PENDING"
	switch {
	case responsibility.Status != "PENDING":
		result.NextAction = "RESPONSIBLE_ACCESSED"
	case result.DeliveryStatus == "FAILED":
		result.NextAction = "CORRECT_RESPONSIBLE_EMAIL"
	case result.DeliveryStatus == "UNKNOWN":
		result.NextAction = "REVIEW_DELIVERY"
	case result.DeliveryStatus == "ACCEPTED" || result.DeliveryStatus == "SENT" || result.DeliveryStatus == "DELIVERED":
		result.NextAction = "WAIT_FOR_RESPONSIBLE"
	default:
		result.NextAction = "WAIT_FOR_DELIVERY"
	}
	return result, nil
}
