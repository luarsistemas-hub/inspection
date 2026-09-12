package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	comparative "inspection/services/inspection/internal/features/analysis/comparative"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	dispatchcapture "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	processmedia "inspection/services/inspection/internal/features/media/process_verified"
	consumeevents "inspection/services/inspection/internal/features/messaging/consume_events"
	dispatchoutbox "inspection/services/inspection/internal/features/messaging/dispatch_outbox"
	delivercritical "inspection/services/inspection/internal/features/notifications/deliver_critical"
	requestdelivery "inspection/services/inspection/internal/features/notifications/request_delivery"
	processdeadline "inspection/services/inspection/internal/features/recapture/process_deadline"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	renderpdf "inspection/services/inspection/internal/features/reports/render_pdf"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/operational"
	"inspection/services/inspection/internal/platform/pdf"
	process "inspection/services/inspection/internal/platform/runtime"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-worker:", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Provider clients are composed here; slices receive only provider-neutral
	// interfaces and cannot construct clients or access provider credentials.
	llmGateway := llm.HTTPGateway{BaseURL: cfg.LiteLLMURL, Client: &http.Client{Timeout: cfg.ProviderTimeout}}
	pdfRenderer := pdf.Gotenberg{BaseURL: cfg.GotenbergURL, Client: &http.Client{Timeout: cfg.ProviderTimeout}}
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	dispatchDB := db
	if cfg.DispatcherDatabaseURL != "" && cfg.DispatcherDatabaseURL != cfg.DatabaseURL {
		dispatchDB, err = database.Open(cfg.DispatcherDatabaseURL)
		if err != nil {
			return err
		}
	}
	connection, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("rabbitmq channel: %w", err)
	}
	defer channel.Close()
	contracts := make([]messaging.QueueContract, 0, 24)
	for _, definition := range [][2]string{{"inspection-created", "inspection.created.v1"}, {"inspection-state", "inspection.state_changed.v1"}, {"participant-channel-projection", "participant.channel_verified.v1"}, {"notification-delivery", "notification.delivery_requested.v1"}, {"notification-status", "notification.channel_status.v1"}, {"origin-invitation", "origin.invitation_requested.v1"}, {"media-upload-completed", "media.upload_completed.v1"}, {"media-verification", "media.verified.v1"}, {"media-screening", "media.screened.v1"}, {"capture-submission", "capture.submitted.v1"}, {"recapture-request", "recapture.requested.v1"}, {"recapture-completion", "recapture.completed.v1"}, {"recapture-deadline", "recapture.deadline_reached.v1"}, {"analysis-comparison-requested", "analysis.comparison_requested.v1"}, {"analysis-comparison-completed", "analysis.comparison_completed.v1"}, {"inspection-classified", "inspection.classified.v1"}, {"report-snapshot-created", "report.snapshot_created.v1"}, {"report-ready", "report.ready.v1"}, {"project-stage-changed", "project.stage_changed.v1"}, {"retention-purge-due", "retention.purge_due.v1"}, {"retention-purged", "retention.purged.v1"}} {
		contract, contractErr := messaging.NewQueueContract(definition[0], definition[1], 32)
		if contractErr != nil {
			return contractErr
		}
		contracts = append(contracts, contract)
	}
	if err := messaging.DeclareTopology(channel, contracts); err != nil {
		return fmt.Errorf("rabbitmq topology: %w", err)
	}
	publisher, err := messaging.NewRabbitPublisher(channel)
	if err != nil {
		return err
	}
	dispatch, err := dispatchoutbox.Setup(dispatchoutbox.Dependencies{DB: dispatchDB, Publisher: publisher, BatchSize: 50})
	if err != nil {
		return err
	}
	minioClient, err := objectstore.NewMinIO(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure)
	if err != nil {
		return err
	}
	privateStore := objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient}
	mediaHandler, err := processmedia.Setup(processmedia.Dependencies{Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient}, Detector: sensitivecontent.NewEmbeddedDetector()})
	if err != nil {
		return err
	}
	channelRegistry, err := notifications.NewRegistry(map[notifications.Channel]notifications.Sender{
		notifications.Email:    notifications.SMTPSender{Address: cfg.SMTPAddress, From: cfg.SMTPFrom},
		notifications.WhatsApp: notifications.TwilioSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioFrom, Channel: notifications.WhatsApp},
		notifications.SMS:      notifications.TwilioSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.TwilioFrom, Channel: notifications.SMS},
	})
	if err != nil {
		return err
	}
	invitationHandler, err := dispatchcapture.Setup(dispatchcapture.Dependencies{Notifications: channelRegistry, CallbackURL: cfg.TwilioCallbackURL})
	if err != nil {
		return err
	}
	deadlineHandler, err := processdeadline.Setup(processdeadline.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	requestJobs, err := requestcomparisons.Setup(requestcomparisons.Dependencies{ModelAlias: cfg.LiteLLMModelAlias, PromptVersion: cfg.LiteLLMPromptVersion, Now: time.Now})
	if err != nil {
		return err
	}
	processJob, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: llmGateway, Now: time.Now, BuildRequest: buildAnalysisRequest(privateStore, cfg.LiteLLMModelAlias, cfg.LiteLLMPromptVersion)})
	if err != nil {
		return err
	}
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	deliverCritical, err := delivercritical.Setup(delivercritical.Dependencies{Registry: channelRegistry, CallbackURL: cfg.TwilioCallbackURL, Now: time.Now})
	if err != nil {
		return err
	}
	createReport, err := generatesnapshot.Setup(generatesnapshot.Dependencies{DB: db, Now: time.Now})
	if err != nil {
		return err
	}
	render, err := renderpdf.Setup(renderpdf.Dependencies{Renderer: pdfRenderer, Store: privateStore, Now: time.Now})
	if err != nil {
		return err
	}
	analysisRequestHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			JobID identity.ID `json:"jobId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return messaging.ErrPermanent
		}
		if payload.JobID != (identity.ID{}) {
			return processJob(ctx, tx, mustPayload(envelope.Payload, payload.JobID), envelope.TenantID)
		}
		return requestJobs(ctx, tx, envelope)
	}
	classifiedHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID   identity.ID `json:"inspectionId"`
			Classification string      `json:"classification"`
			ReasonCodes    []string    `json:"reasonCodes"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var class database.ClassificationRun
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").First(&class).Error; err != nil {
			return err
		}
		var previous database.ClassificationRun
		previousClassification := ""
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").Offset(1).First(&previous).Error; err == nil {
			previousClassification = previous.Classification
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var configuredRecipients []database.NotificationRecipient
		if err := tx.Where("tenant_id=? AND verified=true AND selected=true", envelope.TenantID).Order("channel ASC, destination ASC").Find(&configuredRecipients).Error; err != nil {
			return err
		}
		recipients := make([]requestdelivery.Recipient, 0, len(configuredRecipients))
		for _, recipient := range configuredRecipients {
			recipients = append(recipients, requestdelivery.Recipient{ID: recipient.IdentityID.String(), Channel: recipient.Channel, Destination: recipient.Destination, Internal: true, Verified: recipient.Verified, Selected: recipient.Selected})
		}
		if err := requestdelivery.Request(ctx, tx, requestdelivery.Input{TenantID: envelope.TenantID, InspectionID: payload.InspectionID, Previous: previousClassification, Current: class.Classification, Recipients: recipients, CorrelationID: envelope.CorrelationID}); err != nil {
			return err
		}
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).First(&reference).Error; err != nil {
			return err
		}
		referenceVersion := reference.ReferenceVersionID
		referenceVersionID := "reference-free"
		if referenceVersion != nil {
			referenceVersionID = referenceVersion.String()
		}
		mode := "HISTORICAL"
		var timeline []reportcore.TimelineEntry
		if inspection.ProjectID != nil {
			var project database.Project
			if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, *inspection.ProjectID).First(&project).Error; err == nil {
				if project.ReportMode == "CONSOLIDATED" {
					mode = "CONSOLIDATED"
				}
			}
			var stages []database.ProjectStage
			if err := tx.Where("tenant_id=? AND project_id=?", envelope.TenantID, *inspection.ProjectID).Order("position ASC").Find(&stages).Error; err == nil {
				for _, stage := range stages {
					timeline = append(timeline, reportcore.TimelineEntry{StageID: stage.ID.String(), Status: stage.Status, Position: stage.Position})
				}
			}
		}
		var jobs []database.ComparisonJob
		if err := tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("requirement_key ASC").Find(&jobs).Error; err != nil {
			return err
		}
		findings := make([]reportcore.Finding, 0)
		evidence := make([]reportcore.Evidence, 0)
		for _, job := range jobs {
			var run database.AnalysisRun
			if err := tx.Where("tenant_id=? AND job_id=?", envelope.TenantID, job.ID).Order("created_at DESC").First(&run).Error; err == nil {
				var rows []database.FindingRecord
				if err := tx.Where("tenant_id=? AND analysis_run_id=?", envelope.TenantID, run.ID).Order("created_at ASC").Find(&rows).Error; err != nil {
					return err
				}
				for _, row := range rows {
					var evidenceIDs []string
					_ = json.Unmarshal(row.Evidence, &evidenceIDs)
					findings = append(findings, reportcore.Finding{Category: row.Category, Title: row.Title, Description: row.Description, Severity: row.Severity, Confidence: row.Confidence, EvidenceIDs: evidenceIDs, Quality: row.Quality, RecommendedAction: row.RecommendedAction})
				}
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			var answer database.RequirementAnswer
			if err := tx.Where("tenant_id=? AND requirement_key=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?))", envelope.TenantID, job.RequirementKey, envelope.TenantID, envelope.TenantID, payload.InspectionID).Order("updated_at DESC").First(&answer).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					continue
				}
				return err
			}
			var mediaIDs []identity.ID
			_ = json.Unmarshal(answer.MediaIDs, &mediaIDs)
			var flags []string
			_ = json.Unmarshal(answer.Flags, &flags)
			for _, mediaID := range mediaIDs {
				item := reportcore.Evidence{ID: mediaID.String(), RequirementKey: job.RequirementKey, Flags: append([]string(nil), flags...)}
				var media database.MediaObject
				if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, mediaID).First(&media).Error; err == nil {
					item.Description = media.Description
					item.CaptureSource = media.CaptureSource
					var mediaFlags []string
					if json.Unmarshal(media.Flags, &mediaFlags) == nil && len(mediaFlags) > 0 {
						item.Flags = append(item.Flags, mediaFlags...)
					}
				} else if err != gorm.ErrRecordNotFound {
					return err
				}
				evidence = append(evidence, item)
			}
		}
		result, err := createReport(ctx, generatesnapshot.Input{TenantID: envelope.TenantID, InspectionID: payload.InspectionID, ProjectID: inspection.ProjectID, Mode: mode, Classification: class.Classification, TemplateVersionID: inspection.TemplateVersionID.String(), ReferenceVersionID: referenceVersionID, ProfileVersionID: inspection.AnalysisProfileVersionID.String(), ReasonCodes: payload.ReasonCodes, Timeline: timeline, Coverage: map[string]string{}, Findings: findings, Evidence: evidence})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		eventID := identity.NewID()
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "report.snapshot_created.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"snapshotId": result.SnapshotID, "inspectionId": payload.InspectionID, "version": result.Version}})
	}
	dashboardHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var class database.ClassificationRun
		_ = tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").First(&class).Error
		row := database.DashboardInspection{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: payload.InspectionID, ProjectID: inspection.ProjectID, AssetID: inspection.AssetID, Classification: class.Classification, Status: inspection.Status, Invalidated: inspection.Status == "INVALIDATED", Sequence: time.Now().UnixNano(), UpdatedAt: time.Now().UTC()}
		return tx.Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Assign(row).FirstOrCreate(&row).Error
	}
	retentionHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var hold database.LegalHold
		if err := tx.Where("tenant_id=? AND inspection_id=? AND active", envelope.TenantID, payload.InspectionID).First(&hold).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		// Never trust the fact that an event was published as proof that a
		// retention clock elapsed. Rebuild the authoritative clock from the
		// terminal inspection timestamp and the latest tenant policy so a replay
		// or forged early event cannot delete evidence prematurely.
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		policy := retentioncore.DefaultPolicy()
		var configured database.RetentionPolicy
		if err := tx.Where("tenant_id=?", envelope.TenantID).Order("version DESC").First(&configured).Error; err == nil {
			policy.EvidenceReports = time.Duration(configured.EvidenceDays) * 24 * time.Hour
			policy.Operational = time.Duration(configured.OperationalDays) * 24 * time.Hour
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		clock, err := retentioncore.NewClock(retentioncore.EvidenceReports, inspection.UpdatedAt, time.Time{}, policy)
		if err != nil {
			return err
		}
		if !clock.Eligible(time.Now().UTC()) {
			return gorm.ErrInvalidData
		}
		purge, err := purgedata.PurgeWithStore(ctx, tx, privateStore, envelope.TenantID, payload.InspectionID, clock)
		if err != nil {
			return err
		}
		now := purge.CreatedAt
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: identity.NewID(), Type: "retention.purged.v1", SchemaVersion: 1, OccurredAt: now, TenantID: envelope.TenantID, AggregateID: payload.InspectionID, CorrelationID: envelope.CorrelationID, CausationID: envelope.ID.String(), Payload: map[string]any{"classes": []string{"originals", "parts", "derivatives", "reports", "associations"}}})
	}
	notificationStatusHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			DeliveryID identity.ID `json:"deliveryId"`
			AttemptID  identity.ID `json:"attemptId"`
			Status     string      `json:"status"`
			ReceiptID  string      `json:"receiptId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.DeliveryID == (identity.ID{}) || payload.AttemptID == (identity.ID{}) || payload.Status == "" {
			return messaging.ErrPermanent
		}
		if err := tx.WithContext(ctx).Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=? AND delivery_id=?", envelope.TenantID, payload.AttemptID, payload.DeliveryID).Updates(map[string]any{"status": payload.Status, "receipt_id": payload.ReceiptID, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		var failed, pending, sent int64
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=? AND status='FAILED'", envelope.TenantID, payload.DeliveryID).Count(&failed).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=? AND status='PENDING'", envelope.TenantID, payload.DeliveryID).Count(&pending).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND delivery_id=? AND status IN ('SENT','DELIVERED')", envelope.TenantID, payload.DeliveryID).Count(&sent).Error; err != nil {
			return err
		}
		deliveryStatus := "FAILED"
		if sent > 0 {
			deliveryStatus = "DELIVERED"
		} else if pending > 0 || failed == 0 {
			deliveryStatus = "PENDING"
		}
		return tx.Model(&database.Delivery{}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.DeliveryID).Update("status", deliveryStatus).Error
	}
	handlers := map[string]func(context.Context, *gorm.DB, events.RawEnvelope) error{
		"origin.invitation_requested.v1":     invitationHandler,
		"media.verified.v1":                  mediaHandler,
		"recapture.deadline_reached.v1":      deadlineHandler,
		"capture.submitted.v1":               requestJobs,
		"recapture.completed.v1":             requestJobs,
		"analysis.comparison_requested.v1":   analysisRequestHandler,
		"analysis.comparison_completed.v1":   classify,
		"inspection.classified.v1":           classifiedHandler,
		"report.snapshot_created.v1":         render,
		"report.ready.v1":                    dashboardHandler,
		"notification.delivery_requested.v1": deliverCritical,
		"notification.channel_status.v1":     notificationStatusHandler,
		"retention.purge_due.v1":             retentionHandler,
		"retention.purged.v1": func(_ context.Context, _ *gorm.DB, envelope events.RawEnvelope) error {
			var payload map[string]any
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return messaging.ErrPermanent
			}
			return nil
		},
	}
	consume, err := consumeevents.Setup(consumeevents.Dependencies{DB: db, Connection: connection, Contracts: contracts, Registry: events.DefaultRegistry(), Handlers: handlers})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	if err := operational.Setup(mux, func(r *http.Request) error { return database.Compatible(r.Context(), db, cfg.SchemaMin, cfg.SchemaMax) }, cfg.MetricsToken); err != nil {
		return err
	}
	return process.ServeWithBackground(cfg.HTTPAddress, mux, cfg.ShutdownTimeout, func(ctx context.Context) error {
		errCh := make(chan error, 2)
		go func() { errCh <- dispatch(ctx) }()
		go func() { errCh <- consume(ctx) }()
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		}
	})
}

func mustPayload(_ []byte, jobID identity.ID) []byte {
	data, _ := json.Marshal(map[string]any{"jobId": jobID})
	return data
}

func buildAnalysisRequest(store objectstore.Store, modelAlias, promptVersion string) processcomparison.RequestBuilder {
	return func(ctx context.Context, tx *gorm.DB, job database.ComparisonJob, _ processcomparison.Payload) (llm.StructuredRequest, error) {
		var answers []database.RequirementAnswer
		if err := tx.WithContext(ctx).Where("tenant_id=? AND requirement_key=? AND draft_id IN (SELECT d.id FROM capture.capture_drafts d JOIN inspections.responsibilities r ON r.id=d.responsibility_id WHERE d.tenant_id=? AND r.inspection_id=?)", job.TenantID, job.RequirementKey, job.TenantID, job.InspectionID).Order("updated_at DESC").Limit(1).Find(&answers).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		if len(answers) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: requirement evidence not found")
		}
		var ids []identity.ID
		if err := json.Unmarshal(answers[0].MediaIDs, &ids); err != nil || len(ids) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: no evidence")
		}
		current := make([]comparative.Evidence, 0, len(ids))
		for _, mediaID := range ids {
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=?", job.TenantID, mediaID).Order("created_at DESC").First(&derivative).Error; err != nil {
				return llm.StructuredRequest{}, err
			}
			data, err := store.Read(ctx, derivative.ObjectKey)
			if err != nil {
				return llm.StructuredRequest{}, err
			}
			digest := sha256.Sum256(data)
			current = append(current, comparative.Evidence{ID: mediaID, Source: "CURRENT", Digest: hex.EncodeToString(digest[:]), DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)})
		}
		schema := []byte(`{"type":"object","additionalProperties":false,"required":["noRelevantChange","findings"],"properties":{"noRelevantChange":{"type":"boolean"},"findings":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["category","title","description","severity","confidence","evidenceIds","quality","recommendedAction"],"properties":{"category":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"severity":{"type":"string","enum":["NONE","LOW","MEDIUM","HIGH","CRITICAL"]},"confidence":{"type":"number","minimum":0,"maximum":1},"evidenceIds":{"type":"array","items":{"type":"string"},"minItems":1},"quality":{"type":"string"},"recommendedAction":{"type":"string"}}}}}}`)
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).First(&reference).Error; err != nil {
			return llm.StructuredRequest{}, err
		}
		if reference.ReferenceVersionID == nil || reference.ComparisonMode != "FIXED_ORIGIN" {
			return llm.StructuredRequest{ModelAlias: modelAlias, PromptVersion: promptVersion, JSONSchema: schema, Images: mustCurrentImages(current)}, nil
		}
		var snapshot struct {
			Items []struct {
				MediaID identity.ID `json:"mediaId"`
			} `json:"items"`
		}
		if err := json.Unmarshal(reference.Payload, &snapshot); err != nil || len(snapshot.Items) == 0 {
			return llm.StructuredRequest{}, fmt.Errorf("analysis: pinned origin evidence not found")
		}
		origin := make([]comparative.Evidence, 0, len(snapshot.Items))
		for _, item := range snapshot.Items {
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=?", job.TenantID, item.MediaID).Order("created_at DESC").First(&derivative).Error; err != nil {
				return llm.StructuredRequest{}, err
			}
			data, err := store.Read(ctx, derivative.ObjectKey)
			if err != nil {
				return llm.StructuredRequest{}, err
			}
			digest := sha256.Sum256(data)
			origin = append(origin, comparative.Evidence{ID: item.MediaID, Source: "ORIGIN", Digest: hex.EncodeToString(digest[:]), DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)})
		}
		images, _, err := comparative.Build(current, origin)
		if err != nil {
			return llm.StructuredRequest{}, err
		}
		return llm.StructuredRequest{ModelAlias: modelAlias, PromptVersion: promptVersion, JSONSchema: schema, Images: images}, nil
	}
}

func mustCurrentImages(evidence []comparative.Evidence) []llm.NormalizedImage {
	images := make([]llm.NormalizedImage, 0, len(evidence))
	for _, item := range evidence {
		images = append(images, llm.NormalizedImage{EvidenceID: item.ID.String(), Source: item.Source, Digest: item.Digest, DataURL: item.DataURL})
	}
	return images
}
