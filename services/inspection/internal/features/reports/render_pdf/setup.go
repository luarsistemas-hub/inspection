// Package render_pdf durably schedules and generates private report PDFs.
package render_pdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/pdf"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const rendererVersion = 1

var (
	errPermanentPDF = errors.New("permanent report PDF error")
	// errNotPublished means this attempt lost its reservation or the job was
	// canceled (for example by retention); the produced object was discarded.
	errNotPublished = errors.New("report PDF attempt not published")
)

// finalizeTimeout bounds persistence after the work context ended, so a
// shutdown or timeout still records the outcome instead of leaking the job.
const finalizeTimeout = 15 * time.Second

type Dependencies struct {
	Now func() time.Time
}

// Setup registers idempotent durable work for both audiences. Legacy snapshots
// are acknowledged without regeneration by design.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
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
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.SnapshotID).First(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if snapshot.PDFRenderVersion != rendererVersion {
			return nil
		}
		for _, audience := range []string{"PDF", "PDF_CUSTOMER"} {
			job := database.ReportPDFJob{ID: identity.NewID(), TenantID: envelope.TenantID, SnapshotID: snapshot.ID, Audience: audience, RendererVersion: rendererVersion, Status: "QUEUED", NextAttemptAt: deps.Now().UTC(), CreatedAt: deps.Now().UTC(), UpdatedAt: deps.Now().UTC()}
			if err := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "snapshot_id"}, {Name: "audience"}}, DoNothing: true}).Create(&job).Error; err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// Limits bound the CPU and memory work performed for one report.
type Limits struct {
	Timeout, LeaseDuration, PollInterval time.Duration
	MaxAttempts, MaxEvidence, MaxPages   int
	MaxImageBytes, MaxPDFBytes           int64
	RetryDelays                          []time.Duration
}

// DefaultLimits returns the initial conservative PDF worker budget.
func DefaultLimits() Limits {
	return Limits{Timeout: 60 * time.Second, LeaseDuration: 120 * time.Second, PollInterval: time.Second, MaxAttempts: 4, MaxEvidence: 200, MaxPages: maxPDFPages, MaxImageBytes: 128 << 20, MaxPDFBytes: maxPDFBytes, RetryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 5 * time.Minute}}
}

// Executor runs one reserved document at a time without holding a database
// transaction while reading images, rendering, or uploading the PDF.
type Executor struct {
	db       *gorm.DB
	store    objectstore.Store
	renderer MarotoRenderer
	now      func() time.Time
	limits   Limits
	metrics  *observability.Metrics
}

// SetupExecutor validates worker dependencies and retry/lease limits.
func SetupExecutor(db *gorm.DB, store objectstore.Store, limits Limits, now func() time.Time, metrics ...*observability.Metrics) (*Executor, error) {
	if db == nil || store.Client == nil {
		return nil, fmt.Errorf("reports/render_pdf: missing database or private object store")
	}
	if limits.Timeout <= 0 || limits.LeaseDuration <= limits.Timeout || limits.PollInterval <= 0 || limits.MaxAttempts != len(limits.RetryDelays)+1 || limits.MaxEvidence <= 0 || limits.MaxPages <= 0 || limits.MaxImageBytes <= 0 || limits.MaxPDFBytes <= 0 {
		return nil, fmt.Errorf("reports/render_pdf: invalid worker limits")
	}
	if now == nil {
		now = time.Now
	}
	var observer *observability.Metrics
	if len(metrics) > 0 {
		observer = metrics[0]
	}
	return &Executor{db: db, store: store, renderer: MarotoRenderer{MaxEvidence: limits.MaxEvidence, MaxPages: limits.MaxPages, MaxImageBytes: limits.MaxImageBytes, MaxPDFBytes: limits.MaxPDFBytes}, now: now, limits: limits, metrics: observer}, nil
}

type reservation struct {
	job   database.ReportPDFJob
	token identity.ID
	// previousKey is an object recorded by an earlier, interrupted attempt.
	previousKey string
}

// RunDue handles at most one document. A generation failure is persisted and
// does not terminate the worker's unrelated consumers.
func (e *Executor) RunDue(ctx context.Context) (bool, error) {
	if err := e.updateQueueMetric(ctx); err != nil {
		slog.WarnContext(ctx, "report_pdf_queue_metric_failed", "error", err)
	}
	var reserved *reservation
	for reserved == nil {
		claimed, handled, err := e.reserve(ctx)
		if err != nil {
			return false, err
		}
		if claimed == nil && !handled {
			return false, nil
		}
		reserved = claimed
	}
	if e.metrics != nil {
		e.metrics.ReportPDFAcquire(1)
		defer e.metrics.ReportPDFAcquire(-1)
	}
	started := e.now()
	attrs := []any{"tenantId", reserved.job.TenantID.String(), "jobId", reserved.job.ID.String(), "snapshotId", reserved.job.SnapshotID.String(), "audience", reserved.job.Audience}
	slog.InfoContext(ctx, "report_pdf_job_started", append(attrs, "attempt", reserved.job.Attempts)...)
	if previous := reserved.previousKey; previous != "" {
		e.discard(ctx, reserved.job.TenantID, previous)
	}
	workCtx, cancel := context.WithTimeout(ctx, e.limits.Timeout)
	defer cancel()

	pendingKey := ""
	fail := func(cause error, retryable bool) (bool, error) {
		err := e.finishFailure(ctx, *reserved, cause, retryable, started)
		if pendingKey != "" {
			e.discard(ctx, reserved.job.TenantID, pendingKey)
		}
		return true, err
	}
	snapshot, err := e.readSnapshot(workCtx, *reserved)
	if err != nil {
		return fail(err, !errors.Is(err, gorm.ErrRecordNotFound))
	}
	var value reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &value); err != nil || reportcore.Validate(value) != nil {
		return fail(errPermanentPDF, false)
	}
	_, canonicalDigest, err := reportcore.CanonicalJSON(value)
	if err != nil || !strings.EqualFold(canonicalDigest, snapshot.JSONDigest) {
		return fail(errPermanentPDF, false)
	}
	projected, internal, err := projectForAudience(value, reserved.job.Audience)
	if err != nil {
		return fail(err, false)
	}
	value = projected
	stageStarted := e.now()
	assets, err := e.loadDisplayAssets(workCtx, reserved.job.TenantID, value)
	e.observeStage("images", stageStarted)
	if err != nil {
		return fail(err, !errors.Is(err, errPermanentPDF))
	}
	stageStarted = e.now()
	data, err := e.renderer.Render(workCtx, PDFDocument{SnapshotID: snapshot.ID.String(), Version: snapshot.VersionNumber, Snapshot: value, Internal: internal}, assets)
	e.observeStage("render", stageStarted)
	if err != nil {
		return fail(err, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
	}
	if err := workCtx.Err(); err != nil {
		return fail(err, true)
	}
	artifactObjectID := identity.NewDeterministicID("report-pdf-attempt", reserved.job.ID.String()+":"+reserved.token.String())
	kind := "report-" + strings.ToLower(reserved.job.Audience)
	intendedKey, _ := objectstore.DerivativeKey(reserved.job.TenantID, artifactObjectID, kind, data)
	// The intent is persisted before the upload so a crash never leaves an
	// object that no row can locate.
	if err := e.recordPendingKey(workCtx, *reserved, intendedKey); err != nil {
		if errors.Is(err, errNotPublished) {
			slog.WarnContext(ctx, "report_pdf_job_not_published", append(attrs, "reason", "reservation_lost")...)
			e.observeOutcome(reserved.job.Audience, "CANCELED")
			return true, nil
		}
		return fail(err, true)
	}
	pendingKey = intendedKey
	stageStarted = e.now()
	key, digest, err := e.store.PutDerivative(workCtx, reserved.job.TenantID, artifactObjectID, kind, "application/pdf", data)
	e.observeStage("upload", stageStarted)
	if err != nil {
		return fail(err, true)
	}
	pages := PageCount(data)
	stageStarted = e.now()
	err = e.finishSuccess(ctx, *reserved, snapshot, key, digest, len(data), pages)
	e.observeStage("commit", stageStarted)
	if errors.Is(err, errNotPublished) {
		slog.WarnContext(ctx, "report_pdf_job_not_published", append(attrs, "reason", "reservation_lost_or_canceled")...)
		e.observeOutcome(reserved.job.Audience, "CANCELED")
		return true, nil
	}
	if err != nil {
		e.discard(ctx, reserved.job.TenantID, key)
		return true, err
	}
	slog.InfoContext(ctx, "report_pdf_job_completed", append(attrs, "durationMs", e.now().Sub(started).Milliseconds(), "bytes", len(data), "pages", pages)...)
	return true, nil
}

func (e *Executor) observeOutcome(audience, outcome string) {
	if e.metrics != nil {
		e.metrics.ReportPDFOutcome(audience, outcome, 0, 0)
	}
}

// finalContext detaches persistence from cancellation of the work context.
func finalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
}

// discard removes an uploaded object unless a committed artifact references it.
func (e *Executor) discard(ctx context.Context, tenantID identity.ID, key string) {
	cleanupCtx, cancel := finalContext(ctx)
	defer cancel()
	if err := e.deleteIfOrphan(cleanupCtx, tenantID, key); err != nil {
		slog.WarnContext(ctx, "report_pdf_orphan_cleanup_failed", "tenantId", tenantID.String(), "error", err)
	}
}

func (e *Executor) recordPendingKey(ctx context.Context, reserved reservation, key string) error {
	result := e.db.WithContext(ctx).Model(&database.ReportPDFJob{}).
		Where("tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=? AND lease_expires_at>?", reserved.job.TenantID, reserved.job.ID, reserved.token, e.now().UTC()).
		Updates(map[string]any{"pending_object_key": key, "updated_at": e.now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errNotPublished
	}
	return nil
}

// reserve claims one due job. handled reports whether a row was resolved
// without being claimed (exhausted or canceled), so the caller can continue.
func (e *Executor) reserve(ctx context.Context) (*reservation, bool, error) {
	now := e.now().UTC()
	var claimed *reservation
	var handled, expiredLease, exhausted bool
	var exhaustedAudience string
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job database.ReportPDFJob
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where(
			"(status='QUEUED' AND next_attempt_at<=?) OR (status='PROCESSING' AND lease_expires_at<=?)", now, now,
		).Order("next_attempt_at ASC, created_at ASC").First(&job)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if query.Error != nil {
			return query.Error
		}
		handled = true
		if job.Status == "PROCESSING" {
			expiredLease = true
		}
		var snapshot database.ReportSnapshot
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.SnapshotID).First(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Model(&job).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_expires_at": nil, "last_error": "snapshot_unavailable", "updated_at": now}).Error
			}
			return err
		}
		if snapshot.PDFRenderVersion != rendererVersion || job.RendererVersion != rendererVersion {
			return tx.Model(&job).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_expires_at": nil, "last_error": "renderer_version_unsupported", "updated_at": now}).Error
		}
		if job.Attempts >= e.limits.MaxAttempts {
			if err := tx.Model(&job).Updates(map[string]any{"status": "FAILED", "lease_token": nil, "lease_expires_at": nil, "last_error": "attempts_exhausted", "updated_at": now}).Error; err != nil {
				return err
			}
			exhausted, exhaustedAudience = true, job.Audience
			return emitReadyStatus(tx, events.RawEnvelope{ID: job.ID, TenantID: job.TenantID, CorrelationID: "report-pdf-" + snapshot.ID.String()}, snapshot, job.Audience, "FAILED", now)
		}
		token := identity.NewID()
		leaseUntil := now.Add(e.limits.LeaseDuration)
		result := tx.Model(&job).Where("id=? AND tenant_id=?", job.ID, job.TenantID).Updates(map[string]any{"status": "PROCESSING", "attempts": job.Attempts + 1, "lease_token": token, "lease_expires_at": leaseUntil, "last_error": "", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		previous := job.PendingObjectKey
		job.Status, job.Attempts, job.LeaseToken, job.LeaseExpiresAt = "PROCESSING", job.Attempts+1, &token, &leaseUntil
		claimed = &reservation{job: job, token: token, previousKey: previous}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if e.metrics != nil {
		if expiredLease {
			e.metrics.ReportPDFLeaseExpired()
		}
		if exhausted {
			e.metrics.ReportPDFOutcome(exhaustedAudience, "FAILED", 0, 0)
		}
	}
	return claimed, handled, nil
}

func (e *Executor) readSnapshot(ctx context.Context, reservation reservation) (database.ReportSnapshot, error) {
	var snapshot database.ReportSnapshot
	err := e.db.WithContext(ctx).Where("tenant_id=? AND id=?", reservation.job.TenantID, reservation.job.SnapshotID).First(&snapshot).Error
	return snapshot, err
}

func (e *Executor) loadDisplayAssets(ctx context.Context, tenantID identity.ID, snapshot reportcore.Snapshot) (pdf.AssetBundle, error) {
	if UniqueImageCount(snapshot.Evidence) > e.limits.MaxEvidence {
		return nil, errPermanentPDF
	}
	ids := make([]identity.ID, 0, len(snapshot.Evidence))
	seen := make(map[identity.ID]struct{}, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		if item.Availability == "MISSING" {
			continue
		}
		id, err := identity.ParseID(item.ID)
		if err != nil {
			return nil, errPermanentPDF
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	derivatives := make([]database.MediaDerivative, 0, len(ids))
	if len(ids) > 0 {
		if err := e.db.WithContext(ctx).Where("tenant_id=? AND media_id IN ? AND kind='DISPLAY'", tenantID, ids).Find(&derivatives).Error; err != nil {
			return nil, err
		}
	}
	byID := make(map[identity.ID]database.MediaDerivative, len(derivatives))
	for _, derivative := range derivatives {
		byID[derivative.MediaID] = derivative
	}
	assets := make(pdf.AssetBundle, len(ids))
	var totalBytes int64
	for _, item := range snapshot.Evidence {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.Availability == "MISSING" {
			continue
		}
		mediaID, _ := identity.ParseID(item.ID)
		assetName := "evidence-" + item.ID + ".jpg"
		derivative, ok := byID[mediaID]
		if !ok {
			assets[assetName] = nil
			continue
		}
		if _, alreadyRead := assets[assetName]; alreadyRead {
			continue
		}
		data, err := e.store.Read(ctx, derivative.ObjectKey)
		if err != nil {
			if errors.Is(err, objectstore.ErrNotFound) {
				assets[assetName] = nil
				continue
			}
			if errors.Is(err, objectstore.ErrInvalid) {
				return nil, errPermanentPDF
			}
			return nil, err
		}
		totalBytes += int64(len(data))
		if totalBytes > e.limits.MaxImageBytes {
			return nil, errPermanentPDF
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if (item.DisplayDigest != "" && !strings.EqualFold(item.DisplayDigest, actual)) || (derivative.SHA256 != "" && !strings.EqualFold(derivative.SHA256, actual)) {
			return nil, errPermanentPDF
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 || (derivative.Width > 0 && config.Width != derivative.Width) || (derivative.Height > 0 && config.Height != derivative.Height) || (derivative.SizeBytes > 0 && int64(len(data)) != derivative.SizeBytes) {
			return nil, errPermanentPDF
		}
		assets[assetName] = data
	}
	return assets, nil
}

func (e *Executor) finishSuccess(ctx context.Context, reserved reservation, snapshot database.ReportSnapshot, objectKey, digest string, byteCount, pages int) error {
	ctx, cancel := finalContext(ctx)
	defer cancel()
	now := e.now().UTC()
	notPublished := false
	duplicateKey := ""
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job database.ReportPDFJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", reserved.job.TenantID, reserved.job.ID).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				notPublished = true
				return nil
			}
			return err
		}
		if job.Status != "PROCESSING" || job.LeaseToken == nil || *job.LeaseToken != reserved.token || (job.LeaseExpiresAt != nil && !now.Before(*job.LeaseExpiresAt)) {
			notPublished = true
			return nil
		}
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", job.TenantID, snapshot.InspectionID).First(&inspection).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				notPublished = true
				return tx.Model(&job).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_expires_at": nil, "pending_object_key": "", "last_error": "inspection_purged", "updated_at": now}).Error
			}
			return err
		}
		var existing database.ReportArtifact
		if err := tx.Where("tenant_id=? AND snapshot_id=? AND kind=?", job.TenantID, job.SnapshotID, job.Audience).First(&existing).Error; err == nil {
			if existing.ObjectKey != objectKey {
				duplicateKey = objectKey
			}
			return tx.Model(&job).Updates(map[string]any{"status": "READY", "artifact_id": existing.ID, "lease_token": nil, "lease_expires_at": nil, "pending_object_key": "", "updated_at": now}).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: job.TenantID, SnapshotID: job.SnapshotID, Kind: job.Audience, ObjectKey: objectKey, SHA256: digest, Status: "READY", CreatedAt: now}
		if err := tx.Create(&artifact).Error; err != nil {
			return err
		}
		if err := tx.Model(&job).Updates(map[string]any{"status": "READY", "artifact_id": artifact.ID, "lease_token": nil, "lease_expires_at": nil, "pending_object_key": "", "last_error": "", "updated_at": now}).Error; err != nil {
			return err
		}
		envelope := events.RawEnvelope{ID: reserved.job.ID, TenantID: job.TenantID, CorrelationID: "report-pdf-" + snapshot.ID.String()}
		return emitReady(tx, envelope, snapshot, artifact, now)
	})
	if err != nil {
		return err
	}
	if duplicateKey != "" {
		_ = e.deleteIfOrphan(ctx, reserved.job.TenantID, duplicateKey)
	}
	if notPublished {
		if cleanupErr := e.deleteIfOrphan(ctx, reserved.job.TenantID, objectKey); cleanupErr != nil {
			slog.WarnContext(ctx, "report_pdf_orphan_cleanup_failed", "tenantId", reserved.job.TenantID.String(), "error", cleanupErr)
		}
		return errNotPublished
	}
	if e.metrics != nil {
		e.metrics.ReportPDFOutcome(reserved.job.Audience, "READY", pages, int64(byteCount))
	}
	return nil
}

func (e *Executor) finishFailure(ctx context.Context, reserved reservation, cause error, retryable bool, started time.Time) error {
	logCtx := ctx
	ctx, cancel := finalContext(ctx)
	defer cancel()
	now := e.now().UTC()
	resultStatus := ""
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job database.ReportPDFJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", reserved.job.TenantID, reserved.job.ID).First(&job).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if job.Status != "PROCESSING" || job.LeaseToken == nil || *job.LeaseToken != reserved.token {
			return nil
		}
		status := "FAILED"
		next := now
		if retryable && job.Attempts < e.limits.MaxAttempts {
			status = "QUEUED"
			delayIndex := job.Attempts - 1
			if delayIndex >= 0 && delayIndex < len(e.limits.RetryDelays) {
				next = now.Add(e.limits.RetryDelays[delayIndex])
			}
		}
		resultStatus = status
		errorCode := errorCode(cause)
		if err := tx.Model(&job).Updates(map[string]any{"status": status, "next_attempt_at": next, "lease_token": nil, "lease_expires_at": nil, "pending_object_key": "", "last_error": errorCode, "updated_at": now}).Error; err != nil {
			return err
		}
		if status != "FAILED" {
			return nil
		}
		var snapshot database.ReportSnapshot
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.SnapshotID).First(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		return emitReadyStatus(tx, events.RawEnvelope{ID: reserved.job.ID, TenantID: job.TenantID, CorrelationID: "report-pdf-" + snapshot.ID.String()}, snapshot, job.Audience, "FAILED", now)
	})
	if err == nil && resultStatus != "" && e.metrics != nil {
		if resultStatus == "QUEUED" {
			e.metrics.ReportPDFOutcome(reserved.job.Audience, "RETRY", 0, 0)
		} else {
			e.metrics.ReportPDFOutcome(reserved.job.Audience, "FAILED", 0, 0)
		}
	}
	if err == nil && resultStatus != "" {
		slog.WarnContext(logCtx, "report_pdf_job_failed", "tenantId", reserved.job.TenantID.String(), "jobId", reserved.job.ID.String(), "snapshotId", reserved.job.SnapshotID.String(), "audience", reserved.job.Audience, "attempt", reserved.job.Attempts, "status", resultStatus, "failureCode", errorCode(cause), "durationMs", e.now().Sub(started).Milliseconds())
	}
	return err
}

func (e *Executor) observeStage(stage string, started time.Time) {
	if e.metrics != nil {
		e.metrics.ReportPDFStage(stage, e.now().Sub(started))
	}
}

func (e *Executor) updateQueueMetric(ctx context.Context) error {
	if e.metrics == nil {
		return nil
	}
	var pending int64
	if err := e.db.WithContext(ctx).Model(&database.ReportPDFJob{}).Where("status IN ('QUEUED','PROCESSING')").Count(&pending).Error; err != nil {
		return err
	}
	e.metrics.ReportPDFQueue(pending)
	return nil
}

func (e *Executor) deleteIfOrphan(ctx context.Context, tenantID identity.ID, key string) error {
	var count int64
	if err := e.db.WithContext(ctx).Model(&database.ReportArtifact{}).Where("tenant_id=? AND object_key=?", tenantID, key).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return e.store.Delete(ctx, key)
	}
	return nil
}

func emitReady(tx *gorm.DB, envelope events.RawEnvelope, snapshot database.ReportSnapshot, artifact database.ReportArtifact, now time.Time) error {
	return emitReadyStatus(tx, envelope, snapshot, artifact.Kind, artifact.Status, now)
}

func emitReadyStatus(tx *gorm.DB, envelope events.RawEnvelope, snapshot database.ReportSnapshot, kind, status string, now time.Time) error {
	eventID := identity.NewID()
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "report.ready.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: snapshot.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"snapshotId": snapshot.ID, "inspectionId": snapshot.InspectionID, "kind": kind, "status": status}})
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errPermanentPDF) {
		return "document_invalid_or_limit_exceeded"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "render_timeout"
	}
	if errors.Is(err, objectstore.ErrDenied) {
		return "storage_denied"
	}
	return "temporary_processing_error"
}

func projectForAudience(snapshot reportcore.Snapshot, audience string) (reportcore.Snapshot, bool, error) {
	switch audience {
	case "PDF":
		return snapshot, true, nil
	case "PDF_CUSTOMER":
		snapshot.Findings = nil
		return snapshot, false, nil
	default:
		return reportcore.Snapshot{}, false, errPermanentPDF
	}
}
