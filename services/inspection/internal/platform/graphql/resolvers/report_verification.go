package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type publicVerificationCandidate struct {
	ReportNumber      string
	VersionNumber     int
	JSONDigest        string
	PublicationStatus string
	IssuedAt          time.Time
}

var publicVerificationRateLimit = struct {
	sync.Mutex
	windows map[string]verificationWindow
	queue   []verificationWindowKey
	cleanup int
}{windows: map[string]verificationWindow{}}

type verificationWindow struct {
	started time.Time
	count   int
}

type verificationWindowKey struct {
	client  string
	started time.Time
}

const (
	verificationWindowLimit = 30
	verificationClientLimit = 10000
	verificationCleanupStep = 128
	reissuePendingTTL       = 15 * time.Minute
)

func allowPublicVerification(client string, now time.Time) bool {
	publicVerificationRateLimit.Lock()
	defer publicVerificationRateLimit.Unlock()
	for i := 0; i < verificationCleanupStep && publicVerificationRateLimit.cleanup < len(publicVerificationRateLimit.queue); i++ {
		key := publicVerificationRateLimit.queue[publicVerificationRateLimit.cleanup]
		current, ok := publicVerificationRateLimit.windows[key.client]
		if ok && current.started.Equal(key.started) && now.Sub(current.started) < time.Minute {
			break
		}
		if ok && current.started.Equal(key.started) {
			delete(publicVerificationRateLimit.windows, key.client)
		}
		publicVerificationRateLimit.cleanup++
	}
	if publicVerificationRateLimit.cleanup > 1024 && publicVerificationRateLimit.cleanup*2 > len(publicVerificationRateLimit.queue) {
		publicVerificationRateLimit.queue = append([]verificationWindowKey(nil), publicVerificationRateLimit.queue[publicVerificationRateLimit.cleanup:]...)
		publicVerificationRateLimit.cleanup = 0
	}
	window := publicVerificationRateLimit.windows[client]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		if _, exists := publicVerificationRateLimit.windows[client]; !exists && len(publicVerificationRateLimit.windows) >= verificationClientLimit {
			for publicVerificationRateLimit.cleanup < len(publicVerificationRateLimit.queue) {
				key := publicVerificationRateLimit.queue[publicVerificationRateLimit.cleanup]
				publicVerificationRateLimit.cleanup++
				if current, ok := publicVerificationRateLimit.windows[key.client]; ok && current.started.Equal(key.started) {
					delete(publicVerificationRateLimit.windows, key.client)
					break
				}
			}
		}
		if _, exists := publicVerificationRateLimit.windows[client]; !exists && len(publicVerificationRateLimit.windows) >= verificationClientLimit {
			return false
		}
		window = verificationWindow{started: now}
		publicVerificationRateLimit.queue = append(publicVerificationRateLimit.queue, verificationWindowKey{client: client, started: window.started})
	}
	window.count++
	publicVerificationRateLimit.windows[client] = window
	return window.count <= verificationWindowLimit
}

// VerifyReport exposes only the public identity of an issued version.
func (r *queryResolver) verifyReport(ctx context.Context, code string, version int) (*graphql1.ReportVerification, error) {
	empty := &graphql1.ReportVerification{}
	client, _ := requestctx.ClientIP(ctx)
	if client != "" && !allowPublicVerification(client, time.Now().UTC()) {
		return nil, apperror.New(apperror.RateLimited, "code", "Muitas consultas de verificação. Aguarde um instante e tente novamente.")
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(code)
	if r.DB == nil || version < 1 || decodeErr != nil || len(decoded) != 24 {
		return empty, nil
	}
	codeHash := sha256.Sum256([]byte(code))
	var candidate publicVerificationCandidate
	if err := r.DB.WithContext(ctx).Raw("SELECT report_number, version_number, json_digest, publication_status, issued_at FROM reports.public_verification_lookup(?,?)", hex.EncodeToString(codeHash[:]), version).Scan(&candidate).Error; err != nil {
		return nil, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	if candidate.ReportNumber != "" {
		reportNumber, digest, status, issuedAt, v := candidate.ReportNumber, candidate.JSONDigest, candidate.PublicationStatus, candidate.IssuedAt.UTC().Format(time.RFC3339Nano), candidate.VersionNumber
		return &graphql1.ReportVerification{ReportNumber: &reportNumber, Version: &v, Digest: &digest, Status: &status, IssuedAt: &issuedAt}, nil
	}
	return empty, nil
}

func (r *mutationResolver) reissueReport(ctx context.Context, input graphql1.ReissueReportInput) (*graphql1.ReissueReportPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, true, auth.Manager, auth.Employee); err != nil {
		return nil, err
	}
	inspectionID, err := identity.ParseID(input.InspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	if strings.TrimSpace(input.ClientMutationID) == "" {
		return nil, validationError("clientMutationId", "Informe o identificador desta solicitação.")
	}
	if err := authorizeReportResource(ctx, r.DB, meta, inspectionID, "inspectionId", "Laudo não encontrado."); err != nil {
		return nil, err
	}
	var current database.ReportSnapshot
	// The idempotency key is bound to the inspection: reusing a key on another
	// inspection must reissue it, never replay an unrelated request.
	keySum := sha256.Sum256([]byte(meta.TenantID.String() + ":" + inspectionID.String() + ":" + input.ClientMutationID))
	correlation := "reissue:" + hex.EncodeToString(keySum[:])
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Transaction(func(tx *gorm.DB) error {
			if tx.Dialector.Name() == "postgres" {
				lockKey := "report-reissue:" + meta.TenantID.String() + ":" + inspectionID.String()
				if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", lockKey).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("tenant_id=? AND inspection_id=?", meta.TenantID, inspectionID).Order("version_number DESC").Take(&current).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return apperror.New(apperror.NotFound, "inspectionId", "Laudo não encontrado.")
				}
				return err
			}
			var prior database.AuditEvent
			query := tx.Where("tenant_id=? AND action='report.reissue_requested' AND correlation_id=?", meta.TenantID, correlation).Take(&prior)
			if query.Error == nil {
				return nil
			}
			if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
				return query.Error
			}
			if current.VersionNumber != input.ExpectedVersion {
				return apperror.New(apperror.Conflict, "expectedVersion", "O laudo foi atualizado. Recarregue a versão mais recente e tente novamente.")
			}
			var pending database.AuditEvent
			pendingErr := tx.Where("tenant_id=? AND action='report.reissue_requested' AND target_id=? AND reason=? AND outcome='QUEUED' AND occurred_at>=?", meta.TenantID, inspectionID.String(), fmt.Sprintf("expectedVersion=%d", input.ExpectedVersion), time.Now().UTC().Add(-reissuePendingTTL)).Take(&pending).Error
			if pendingErr == nil {
				return apperror.New(apperror.Conflict, "expectedVersion", "Já existe uma solicitação de atualização deste laudo. Aguarde a conclusão e tente novamente.")
			}
			if !errors.Is(pendingErr, gorm.ErrRecordNotFound) {
				return pendingErr
			}
			if _, err := requestinventoryjobs.Reanalyze(ctx, tx, meta.TenantID, inspectionID, time.Now().UTC()); err != nil {
				return err
			}
			actor := meta.Principal.MembershipID
			if actor == (identity.ID{}) {
				actor = meta.TenantID
			}
			now := time.Now().UTC()
			if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: meta.TenantID, ActorID: actor, Action: "report.reissue_requested", TargetType: "inspection", TargetID: inspectionID.String(), Outcome: "QUEUED", Reason: fmt.Sprintf("expectedVersion=%d", input.ExpectedVersion), CorrelationID: correlation, OccurredAt: now}).Error; err != nil {
				return err
			}
			payload, err := json.Marshal(map[string]any{"inspectionId": inspectionID, "force": true})
			if err != nil {
				return err
			}
			return messaging.AddOutbox(tx, events.Envelope[json.RawMessage]{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: meta.TenantID, AggregateID: inspectionID, CorrelationID: correlation, Payload: payload})
		})
	})
	if err != nil {
		return nil, err
	}
	return &graphql1.ReissueReportPayload{Report: mapReport(current), UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}
