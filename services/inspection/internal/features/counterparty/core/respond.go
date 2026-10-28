package core

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/observability"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Respond records the single acknowledgment or contestation allowed for a version.
func (s Service) Respond(ctx context.Context, owner SessionOwner, response, statement, mutationID string) (database.CounterpartyInvitation, error) {
	if !utf8.ValidString(mutationID) || len(mutationID) > 200 || strings.IndexFunc(mutationID, unicode.IsControl) >= 0 {
		return database.CounterpartyInvitation{}, apperror.New(apperror.InvalidInput, "clientMutationId", "client mutation id must contain at most 200 characters")
	}
	var result database.CounterpartyInvitation
	err := s.within(ctx, owner.TenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", owner.TenantID, owner.InvitationID).First(&result).Error; err != nil {
			return safeInvitationLookupError(err, "counterpartyInvitationId")
		}
		if response != "ACKNOWLEDGED" && response != "CONTESTED" {
			return apperror.New(apperror.InvalidInput, "response", "invalid counterparty response")
		}
		if response == "CONTESTED" {
			validatedStatement, err := validateStatement(statement)
			if err != nil {
				return err
			}
			statement = validatedStatement
			if err := lockContestationSources(tx, owner.TenantID, result.OriginVersionID); err != nil {
				return err
			}
		}
		if result.Response != "PENDING" {
			if result.Response == "NO_RESPONSE" {
				return apperror.New(apperror.DeadlinePassed, "deadlineAt", "deadline passed")
			}
			if result.Response == "ACKNOWLEDGED" && response == "ACKNOWLEDGED" {
				return nil
			}
			if result.Response == "CONTESTED" && response == "CONTESTED" && result.Statement != nil && *result.Statement == statement && result.ResponseMutationID != nil && *result.ResponseMutationID == mutationID {
				return nil
			}
			return apperror.New(apperror.AlreadyResponded, "response", "already responded")
		}
		now := s.now()
		if !deadlineOpen(now, result.DeadlineAt) {
			return apperror.New(apperror.DeadlinePassed, "deadlineAt", "deadline passed")
		}
		result.Response = response
		result.RespondedAt = &now
		result.ResponseMutationID = &mutationID
		if response == "CONTESTED" {
			result.Statement = &statement
		}
		result.UpdatedAt = now
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		payload := events.CounterpartyRespondedPayload{CounterpartyInvitationID: result.ID, OriginVersionID: result.OriginVersionID}
		envelope := events.Envelope[events.CounterpartyRespondedPayload]{ID: identity.NewID(), Type: "counterparty.responded.v1", SchemaVersion: 1, OccurredAt: now, TenantID: result.TenantID, AggregateID: result.ID, CorrelationID: "counterparty-" + result.ID.String(), Payload: payload}
		return messaging.AddOutbox(tx, envelope)
	})
	if err == nil && result.RespondedAt != nil {
		observability.LogCounterpartyResponded(ctx, result.TenantID, result.ID, result.OriginVersionID, result.Response)
	}
	return result, err
}

func lockContestationSources(tx *gorm.DB, tenantID, versionID identity.ID) error {
	mediaIDs, err := retentioncore.VersionSourceMediaIDs(tx, tenantID, []identity.ID{versionID})
	if err != nil {
		return err
	}
	for _, mediaID := range mediaIDs {
		var source database.VideoSource
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Take(&source).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if source.Status == "DELETING" {
			return apperror.New(apperror.Conflict, "counterpartyInvitationId", "evidence deletion is in progress; contestation was not recorded")
		}
	}
	return nil
}

func validateStatement(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !utf8.ValidString(trimmed) || len([]rune(trimmed)) > 4000 || strings.IndexFunc(trimmed, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) >= 0 {
		return "", apperror.New(apperror.InvalidInput, "statement", "statement must contain 1 to 4000 characters")
	}
	return trimmed, nil
}

func deadlineOpen(now, deadline time.Time) bool { return now.Before(deadline) }
