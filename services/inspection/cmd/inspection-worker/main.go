package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	requestcomparisons "inspection/services/inspection/internal/features/analysis/request_comparisons"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	dispatchcapture "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	processuploadcompleted "inspection/services/inspection/internal/features/media/process_upload_completed"
	processmedia "inspection/services/inspection/internal/features/media/process_verified"
	consumeevents "inspection/services/inspection/internal/features/messaging/consume_events"
	dispatchoutbox "inspection/services/inspection/internal/features/messaging/dispatch_outbox"
	alertdelivery "inspection/services/inspection/internal/features/notifications/alert_delivery"
	consumedelivery "inspection/services/inspection/internal/features/notifications/consume_delivery"
	executedelivery "inspection/services/inspection/internal/features/notifications/execute_delivery"
	recordinapp "inspection/services/inspection/internal/features/notifications/record_in_app"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	originpromote "inspection/services/inspection/internal/features/origins/promote_inspection"
	processdeadline "inspection/services/inspection/internal/features/recapture/process_deadline"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	handleclassified "inspection/services/inspection/internal/features/reports/handle_classified"
	handlereportready "inspection/services/inspection/internal/features/reports/handle_report_ready"
	renderpdf "inspection/services/inspection/internal/features/reports/render_pdf"
	purgedata "inspection/services/inspection/internal/features/retention/purge_data"
	"inspection/services/inspection/internal/features/templates/catalog"
	recordllmcall "inspection/services/inspection/internal/features/usage/record_llm_call"
	"inspection/services/inspection/internal/platform/config"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/operational"
	process "inspection/services/inspection/internal/platform/runtime"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

// workerQueueDefinitions are the durable queues of the ordinary worker and
// the event each one receives. Video condition requests are deliberately
// absent: they belong to the dedicated video worker queue.
var workerQueueDefinitions = [][2]string{{"inspection-created", "inspection.created.v1"}, {"inspection-state", "inspection.state_changed.v1"}, {"participant-channel-projection", "participant.channel_verified.v1"}, {"notification-delivery-v2", "notification.delivery_requested.v2"}, {"notification-delivery-terminal", "notification.delivery_terminal.v1"}, {"origin-invitation", "origin.invitation_requested.v1"}, {"origin-promotion", "origin.promotion_requested.v1"}, {"media-upload-completed", "media.upload_completed.v1"}, {"media-verification", "media.verified.v1"}, {"media-screening", "media.screened.v1"}, {"capture-submission", "capture.submitted.v1"}, {"recapture-request", "recapture.requested.v1"}, {"recapture-completion", "recapture.completed.v1"}, {"recapture-deadline", "recapture.deadline_reached.v1"}, {"analysis-comparison-requested", "analysis.comparison_requested.v1"}, {"inspection.video", "analysis.video_comparison_requested.v1"}, {"analysis-comparison-completed", "analysis.comparison_completed.v1"}, {"inspection-classified", "inspection.classified.v1"}, {"report-snapshot-created", "report.snapshot_created.v1"}, {"report-ready", "report.ready.v1"}, {"project-stage-changed", "project.stage_changed.v1"}, {"retention-purge-due", "retention.purge_due.v1"}, {"retention-purged", "retention.purged.v1"}}

// ordinaryQueueContracts drops the dedicated video condition queue: the
// ordinary worker declares it for topology but never consumes video payloads.
func ordinaryQueueContracts(contracts []messaging.QueueContract) []messaging.QueueContract {
	ordinary := make([]messaging.QueueContract, 0, len(contracts))
	for _, contract := range contracts {
		if contract.RoutingKey != "analysis.video_comparison_requested.v1" {
			ordinary = append(ordinary, contract)
		}
	}
	return ordinary
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspection-worker:", err)
		os.Exit(1)
	}
}

func resolveClassifiedVideoPlan(tx *gorm.DB, tenantID, inspectionID identity.ID, videoPlanID *identity.ID) (database.VideoPlan, error) {
	if videoPlanID == nil || *videoPlanID == (identity.ID{}) {
		return database.VideoPlan{}, messaging.ErrPermanent
	}
	var plan database.VideoPlan
	err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", tenantID, *videoPlanID, inspectionID).Take(&plan).Error
	return plan, err
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Provider clients are composed here; slices receive only provider-neutral
	// interfaces and cannot construct clients or access provider credentials.
	llmAPIKey := ""
	if cfg.LLMMode == "live" {
		llmAPIKey = cfg.LiteLLMAPIKey
	}
	metrics := observability.NewMetrics()
	llmLogger := observability.NewJSONLLMLogger(os.Stdout, "inspection-worker", cfg.Environment, cfg.LLMMode)
	llmTransport := llm.HTTPGateway{BaseURL: cfg.LiteLLMURL, APIKey: llmAPIKey, Client: &http.Client{Timeout: cfg.ProviderTimeout}}
	operationalGateway, err := operationalNotificationGateway(cfg)
	if err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	runtimeDB := db
	if cfg.RuntimeDatabaseURL != "" && cfg.RuntimeDatabaseURL != cfg.DatabaseURL {
		runtimeDB, err = database.Open(cfg.RuntimeDatabaseURL)
		if err != nil {
			return fmt.Errorf("runtime database: %w", err)
		}
		if sqlDB, dbErr := runtimeDB.DB(); dbErr == nil {
			defer sqlDB.Close()
		}
	}
	callLedger, err := recordllmcall.Setup(recordllmcall.Dependencies{DB: runtimeDB})
	if err != nil {
		return err
	}
	llmGateway := llm.ObservedGateway{Inner: llmTransport, Metrics: metrics, Logger: llmLogger, Mode: cfg.LLMMode, Ledger: callLedger}
	payloadCipher, err := notifications.NewPayloadCipher(cfg.Notification.ActivePayloadKey, cfg.Notification.PayloadKeys)
	if err != nil {
		return err
	}
	workerObserver := observability.NewWorkerObserver(metrics, llmLogger)
	providerResolver, err := notifications.NewProviderResolver(notifications.Provider(cfg.Notification.WhatsAppProvider))
	if err != nil {
		return err
	}
	notificationService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: db, Providers: providerResolver, Payloads: payloadCipher, Metrics: metrics, EnabledChannels: cfg.Notification.EnabledChannels})
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
	contracts := make([]messaging.QueueContract, 0, len(workerQueueDefinitions))
	for _, definition := range workerQueueDefinitions {
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
	dispatch, err := dispatchoutbox.Setup(dispatchoutbox.Dependencies{DB: dispatchDB, Publisher: publisher, BatchSize: 50, Metrics: metrics})
	if err != nil {
		return err
	}
	minioClient, err := objectstore.NewMinIOWithRegion(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOSecure, cfg.MinIORegion)
	if err != nil {
		return err
	}
	privateStore := objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient}
	detector := sensitivecontent.NewEmbeddedDetector()
	if !cfg.ImageValidationEnabled {
		log.Printf("inspection-worker: image validation disabled by INSPECTION_IMAGE_VALIDATION_ENABLED")
		detector = sensitivecontent.NewDisabledDetector()
	}
	mediaHandler, err := processmedia.Setup(processmedia.Dependencies{Store: objectstore.Store{Bucket: cfg.MinIOBucket, Client: minioClient}, Detector: detector})
	if err != nil {
		return err
	}
	uploadCompletedHandler, err := processuploadcompleted.Setup(processuploadcompleted.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	invitationHandler, err := dispatchcapture.Setup(dispatchcapture.Dependencies{Notifications: notificationService, CaptureBaseURL: cfg.CaptureOrigin})
	if err != nil {
		return err
	}
	deadlineHandler, err := processdeadline.Setup(processdeadline.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	requestJobs, err := requestcomparisons.Setup(requestcomparisons.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	requestBuilder, err := buildrequest.Setup(buildrequest.Dependencies{Store: privateStore})
	if err != nil {
		return err
	}
	processJob, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: llmGateway, Now: time.Now, BuildRequest: requestBuilder, Metrics: metrics, Logger: llmLogger, Mode: cfg.LLMMode})
	if err != nil {
		return err
	}
	classify, err := classifyinspection.Setup(classifyinspection.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	deliveryExecutor, err := executedelivery.Setup(executedelivery.Dependencies{DB: db, Gateway: operationalGateway, MaxAttempts: cfg.Notification.MaxAttempts, RetryDelays: cfg.Notification.RetryDelays, LeaseDuration: cfg.ProviderTimeout + 5*time.Second, CallbackURL: cfg.TwilioCallbackURL, ProviderAccounts: map[notifications.Provider]string{notifications.ProviderTwilio: cfg.TwilioAccountSID, notifications.ProviderMeta: cfg.Notification.MetaPhoneNumberID}, Payloads: payloadCipher, Metrics: metrics, EnabledChannels: cfg.Notification.EnabledChannels})
	if err != nil {
		return err
	}
	terminalAlert, err := alertdelivery.Setup(alertdelivery.Dependencies{Notifications: notificationService, DashboardURL: cfg.AdminOrigin, Metrics: metrics})
	if err != nil {
		return err
	}
	inAppNotification := recordinapp.Setup()
	createReport, err := generatesnapshot.Setup(generatesnapshot.Dependencies{DB: db, Now: time.Now})
	if err != nil {
		return err
	}
	render, err := renderpdf.Setup(renderpdf.Dependencies{Now: time.Now})
	if err != nil {
		return err
	}
	pdfLimits := renderpdf.DefaultLimits()
	pdfLimits.Timeout = cfg.ReportPDF.Timeout
	pdfLimits.LeaseDuration = cfg.ReportPDF.LeaseDuration
	pdfLimits.PollInterval = cfg.ReportPDF.PollInterval
	pdfLimits.MaxAttempts = cfg.ReportPDF.MaxAttempts
	pdfLimits.MaxEvidence = cfg.ReportPDF.MaxEvidence
	pdfLimits.MaxPages = cfg.ReportPDF.MaxPages
	pdfLimits.MaxImageBytes = cfg.ReportPDF.MaxImageBytes
	pdfLimits.MaxPDFBytes = cfg.ReportPDF.MaxPDFBytes
	pdfLimits.RetryDelays = cfg.ReportPDF.RetryDelays
	pdfExecutor, err := renderpdf.SetupExecutor(db, privateStore, pdfLimits, time.Now, cfg.AdminOrigin, metrics)
	if err != nil {
		return err
	}
	analysisRequestHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			JobID           identity.ID `json:"jobId"`
			ComparisonJobID identity.ID `json:"comparisonJobId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return messaging.ErrPermanent
		}
		jobID := payload.JobID
		if jobID == (identity.ID{}) {
			jobID = payload.ComparisonJobID
		}
		if jobID != (identity.ID{}) {
			return processJob(ctx, tx, mustPayload(envelope.Payload, jobID), envelope.TenantID)
		}
		return requestJobs(ctx, tx, envelope)
	}
	promotionHandler := func(ctx context.Context, _ *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			PromotionID identity.ID `json:"promotionId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.PromotionID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		return originpromote.Process(ctx, runtimeDB, privateStore, envelope.TenantID, payload.PromotionID)
	}
	classifiedHandler, err := handleclassified.Setup(handleclassified.Dependencies{Notifications: notificationService, CreateSnapshot: createReport, Now: time.Now})
	if err != nil {
		return err
	}

	reportReadyHandler, err := handlereportready.Setup(handlereportready.Dependencies{InAppNotification: inAppNotification, UpsertTriage: handleclassified.UpsertTriageReviewCase, Now: time.Now})
	if err != nil {
		return err
	}

	retentionHandler := func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		return purgedata.HandleDueEvent(ctx, tx, privateStore, envelope)
	}
	handlers := map[string]func(context.Context, *gorm.DB, events.RawEnvelope) error{
		"inspection.created.v1": inspectionCreatedHandler(inAppNotification),
		"inspection.state_changed.v1": func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
			var payload struct {
				InspectionID identity.ID `json:"inspectionId"`
			}
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) {
				return messaging.ErrPermanent
			}
			var inspection database.Inspection
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			if inspection.Status != "CANCELED" && inspection.Status != "INVALIDATED" {
				return nil
			}
			var triage database.TriageCase
			if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Take(&triage).Error; err == gorm.ErrRecordNotFound {
				return nil
			} else if err != nil {
				return err
			}
			if triage.Status == "ARCHIVED" {
				return nil
			}
			now := time.Now().UTC()
			if err := tx.WithContext(ctx).Model(&triage).Updates(map[string]any{"status": "ARCHIVED", "assignee_id": nil, "version": triage.Version + 1, "updated_at": now}).Error; err != nil {
				return err
			}
			return tx.WithContext(ctx).Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: envelope.TenantID, CaseID: triage.ID, ActorID: envelope.TenantID, Kind: "ARCHIVED", Body: "Vistoria cancelada ou invalidada.", CreatedAt: now}).Error
		},
		"origin.invitation_requested.v1": invitationHandler,
		"origin.promotion_requested.v1":  promotionHandler,
		"media.upload_completed.v1":      uploadCompletedHandler,
		"media.verified.v1":              mediaHandler,
		"recapture.deadline_reached.v1":  deadlineHandler,
		"recapture.requested.v1":         inAppNotification,
		"capture.submitted.v1":           requestJobs,
		"recapture.completed.v1": func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
			if err := requestJobs(ctx, tx, envelope); err != nil {
				return err
			}
			return inAppNotification(ctx, tx, envelope)
		},
		"analysis.comparison_requested.v1":   analysisRequestHandler,
		"analysis.comparison_completed.v1":   classify,
		"inspection.classified.v1":           classifiedHandler,
		"report.snapshot_created.v1":         render,
		"report.ready.v1":                    reportReadyHandler,
		"notification.delivery_requested.v2": consumedelivery.Setup(),
		"notification.delivery_terminal.v1":  terminalAlert,
		"retention.purge_due.v1":             retentionHandler,
		"retention.purged.v1":                purgedata.HandlePurgedEvent,
	}
	ordinaryContracts := ordinaryQueueContracts(contracts)
	consume, err := consumeevents.Setup(consumeevents.Dependencies{DB: db, Connection: connection, Contracts: ordinaryContracts, Registry: events.DefaultRegistry(), Handlers: handlers, Observer: workerObserver, DeliveryObserver: workerObserver})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	if err := operational.SetupWithMetrics(mux, func(r *http.Request) error { return database.Compatible(r.Context(), db, cfg.SchemaMin, cfg.SchemaMax) }, cfg.MetricsToken, metrics); err != nil {
		return err
	}
	return process.ServeWithBackground(cfg.HTTPAddress, mux, cfg.ShutdownTimeout, func(ctx context.Context) error {
		errCh := make(chan error, 4)
		go func() { errCh <- dispatch(ctx) }()
		go func() { errCh <- consume(ctx) }()
		go func() {
			ticker := time.NewTicker(1000 * time.Millisecond)
			defer ticker.Stop()
			for {
				if _, err := deliveryExecutor.RunDue(ctx); err != nil {
					errCh <- err
					return
				}
				select {
				case <-ctx.Done():
					errCh <- nil
					return
				case <-ticker.C:
				}
			}
		}()
		go func() {
			ticker := time.NewTicker(cfg.ReportPDF.PollInterval)
			defer ticker.Stop()
			for {
				worked, err := pdfExecutor.RunDue(ctx)
				if err != nil && ctx.Err() == nil {
					// Database hiccups must not take down unrelated consumers.
					log.Printf("report PDF executor: %v", err)
				} else if worked && ctx.Err() == nil {
					continue
				}
				select {
				case <-ctx.Done():
					errCh <- nil
					return
				case <-ticker.C:
				}
			}
		}()
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			return err
		}
	})
}

func inspectionCreatedHandler(inApp func(context.Context, *gorm.DB, events.RawEnvelope) error) func(context.Context, *gorm.DB, events.RawEnvelope) error {
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		if err := consumeevents.HandleLifecycleEvent(ctx, tx, envelope); err != nil {
			return err
		}
		return inApp(ctx, tx, envelope)
	}
}

// operationalNotificationGateway composes operational providers in the worker
// only. Legacy OTP registries remain independent by design.
func operationalNotificationGateway(cfg config.Config) (*notifications.Gateway, error) {
	adapters := map[notifications.Channel]map[notifications.Provider]notifications.Adapter{
		notifications.Email: {
			notifications.ProviderSMTP: notifications.SMTPAdapter(notifications.SMTPSender{Address: cfg.SMTPAddress, From: cfg.SMTPFrom, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, ReplyTo: cfg.SMTPReplyTo, TLSMode: cfg.Notification.SMTPTLSMode, Timeout: cfg.ProviderTimeout}),
		},
	}
	if cfg.Notification.EnabledChannels["SMS"] {
		adapters[notifications.SMS] = map[notifications.Provider]notifications.Adapter{
			notifications.ProviderTwilio: notifications.TwilioSMSSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.Notification.TwilioSMSFrom, StatusCallback: cfg.TwilioCallbackURL, Client: &http.Client{Timeout: cfg.ProviderTimeout}},
		}
	}
	if cfg.Notification.EnabledChannels["WHATSAPP"] {
		adapters[notifications.WhatsApp] = map[notifications.Provider]notifications.Adapter{
			notifications.ProviderTwilio: notifications.TwilioWhatsAppSender{TwilioSMSSender: notifications.TwilioSMSSender{BaseURL: cfg.TwilioBaseURL, AccountSID: cfg.TwilioAccountSID, AuthToken: cfg.TwilioAuthToken, From: cfg.Notification.TwilioWhatsAppFrom, StatusCallback: cfg.TwilioCallbackURL, Client: &http.Client{Timeout: cfg.ProviderTimeout}}, ContentSIDs: cfg.Notification.TwilioTemplates},
			notifications.ProviderMeta:   notifications.MetaWhatsAppSender{BaseURL: cfg.Notification.MetaBaseURL, APIVersion: cfg.Notification.MetaAPIVersion, PhoneNumberID: cfg.Notification.MetaPhoneNumberID, AccessToken: cfg.Notification.MetaAccessToken, Templates: cfg.Notification.MetaTemplates, Client: &http.Client{Timeout: cfg.ProviderTimeout}},
		}
	}
	return notifications.NewGateway(adapters)
}

func mustPayload(_ []byte, jobID identity.ID) []byte {
	data, _ := json.Marshal(map[string]any{"jobId": jobID})
	return data
}

// reportSnapshotContext freezes the human-readable context and reference
// evidence needed to understand a report after operational records evolve.
func reportSnapshotContext(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, reference database.ReferenceSnapshot, generatedAt time.Time) (reportcore.Context, []reportcore.Requirement, []reportcore.Evidence, error) {
	var asset database.Asset
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.AssetID).First(&asset).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var participant database.Participant
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.ParticipantID).First(&participant).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var templateRow database.Template
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.TemplateID).First(&templateRow).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	var version database.TemplateVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).First(&version).Error; err != nil {
		return reportcore.Context{}, nil, nil, err
	}

	context := reportcore.Context{Asset: reportcore.AssetContext{ID: asset.ID.String(), Name: asset.Name, ExternalKey: asset.ExternalKey, Address: asset.Address}, Participant: reportcore.ParticipantContext{ID: participant.ID.String(), Name: participant.Name}, Template: reportcore.TemplateContext{ID: templateRow.ID.String(), Name: templateRow.Name, Version: version.VersionNumber}, Inspection: reportcore.InspectionContext{DueAt: inspection.DueAt.UTC().Format(time.RFC3339Nano), GeneratedAt: generatedAt.UTC().Format(time.RFC3339Nano)}}
	if inspection.ProjectID != nil {
		context.Inspection.ProjectID = inspection.ProjectID.String()
	}
	if inspection.StageID != nil {
		context.Inspection.StageID = inspection.StageID.String()
		var stage database.ProjectStage
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *inspection.StageID).First(&stage).Error; err == nil {
			context.Inspection.StageLabel = stage.Label
		} else if err != gorm.ErrRecordNotFound {
			return reportcore.Context{}, nil, nil, err
		}
	}
	var submission database.SubmissionVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspection.ID).Order("submitted_at DESC").First(&submission).Error; err == nil {
		context.Inspection.SubmittedAt = submission.SubmittedAt.UTC().Format(time.RFC3339Nano)
	} else if err != gorm.ErrRecordNotFound {
		return reportcore.Context{}, nil, nil, err
	}

	requirements, err := reportSnapshotRequirements(ctx, tx, tenantID, inspection.ID, version.DefinitionJSON)
	if err != nil {
		return reportcore.Context{}, nil, nil, err
	}

	var payload struct {
		Items []struct {
			MediaID     identity.ID `json:"mediaId"`
			Category    string      `json:"category"`
			Description string      `json:"description"`
		} `json:"items"`
	}
	if err := json.Unmarshal(reference.Payload, &payload); err != nil {
		return reportcore.Context{}, nil, nil, err
	}
	evidence := make([]reportcore.Evidence, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.MediaID == (identity.ID{}) {
			continue
		}
		requirementKey := item.Category
		if reference.ComparisonMode == "FIXED_ORIGIN" {
			requirementKey = "origin:" + item.MediaID.String()
		}
		var media database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, item.MediaID).First(&media).Error; err == gorm.ErrRecordNotFound {
			evidence = append(evidence, reportcore.Evidence{ID: item.MediaID.String(), RequirementKey: requirementKey, Role: "REFERENCE", Description: item.Description, Availability: "MISSING"})
			continue
		} else if err != nil {
			return reportcore.Context{}, nil, nil, err
		}
		value := reportcore.Evidence{ID: media.ID.String(), RequirementKey: requirementKey, Role: "REFERENCE", Description: item.Description, CaptureSource: media.CaptureSource, Availability: "MISSING"}
		if value.Description == "" {
			value.Description = media.Description
		}
		if media.CapturedAt != nil {
			value.CapturedAt = media.CapturedAt.UTC().Format(time.RFC3339Nano)
		}
		var derivative database.MediaDerivative
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind=?", tenantID, media.ID, "DISPLAY").First(&derivative).Error; err == nil {
			value.DisplayDigest, value.Availability = derivative.SHA256, "AVAILABLE"
		} else if err != gorm.ErrRecordNotFound {
			return reportcore.Context{}, nil, nil, err
		}
		var flags []string
		_ = json.Unmarshal(media.Flags, &flags)
		value.Flags = flags
		evidence = append(evidence, value)
	}
	return context, requirements, evidence, nil
}

func reportVideoPlanEvidence(ctx context.Context, tx *gorm.DB, tenantID, planItemID identity.ID) ([]reportcore.Evidence, error) {
	var item database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, planItemID).Take(&item).Error; err != nil {
		return nil, err
	}
	var memberships []database.VideoPlanItemFrame
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_item_id=?", tenantID, item.ID).Order("core_group ASC,role ASC,pair_order ASC").Find(&memberships).Error; err != nil {
		return nil, err
	}
	evidence := make([]reportcore.Evidence, 0, len(memberships))
	for _, membership := range memberships {
		var frame database.VideoFrame
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, membership.FrameID).Take(&frame).Error; err != nil {
			return nil, err
		}
		var source database.VideoSource
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, frame.SourceMediaID).Take(&source).Error; err != nil {
			return nil, err
		}
		evidence = append(evidence, reportVideoFrameEvidence(item, membership, frame, source))
	}
	return evidence, nil
}

func loadReportVideoPlanJobs(ctx context.Context, tx *gorm.DB, tenantID, inspectionID, planID identity.ID) ([]database.ComparisonJob, error) {
	var jobs []database.ComparisonJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND video_plan_id=?", tenantID, inspectionID, planID).Order("requirement_key ASC,video_core_group ASC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	var items []database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=? AND carried_result_job_id IS NOT NULL", tenantID, planID).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return jobs, nil
	}
	var plan database.VideoPlan
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, planID).Take(&plan).Error; err != nil {
		return nil, err
	}
	if plan.ParentPlanID == nil {
		return nil, messaging.ErrPermanent
	}
	ancestorIDs := make([]identity.ID, 0, 4)
	seen := map[identity.ID]struct{}{plan.ID: {}}
	current := plan
	for depth := 0; current.ParentPlanID != nil; depth++ {
		if depth >= 32 {
			return nil, messaging.ErrPermanent
		}
		parentID := *current.ParentPlanID
		if _, exists := seen[parentID]; exists {
			return nil, messaging.ErrPermanent
		}
		seen[parentID] = struct{}{}
		ancestorIDs = append(ancestorIDs, parentID)
		var ancestor database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=?", tenantID, parentID, inspectionID).Take(&ancestor).Error; err != nil {
			return nil, err
		}
		current = ancestor
	}
	for _, item := range items {
		var carried database.ComparisonJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND video_plan_id IN ? AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", tenantID, *item.CarriedResultJobID, inspectionID, ancestorIDs).Take(&carried).Error; err != nil {
			return nil, err
		}
		jobs = append(jobs, carried)
	}
	return jobs, nil
}

func reportVideoFrameEvidence(item database.VideoPlanItem, membership database.VideoPlanItemFrame, frame database.VideoFrame, source database.VideoSource) reportcore.Evidence {
	role := "CURRENT"
	if membership.Role == "ORIGIN" {
		role = "REFERENCE"
	}
	ptsMicros := frame.PTSMicros
	timestampMS := ptsMicros / 1000
	return reportcore.Evidence{
		ID: frame.ID.String(), RequirementKey: item.RequirementKey, Role: role,
		EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(),
		PreparationID: frame.PreparationID.String(), PTSMicros: &ptsMicros, TimestampMS: &timestampMS, FrameDigest: frame.SHA256,
		CaptureSource: "VIDEO", DisplayDigest: frame.SHA256, Availability: "AVAILABLE", SourceAvailability: source.Status,
		Description: fmt.Sprintf("Quadro do vídeo em %d ms", timestampMS),
	}
}

func reportSnapshotRequirements(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, templateDefinition []byte) ([]reportcore.Requirement, error) {
	var draft database.CaptureDraft
	err := tx.WithContext(ctx).
		Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID).
		Order("updated_at DESC").First(&draft).Error
	if err == nil {
		return reportRequirementsFromCapture(draft.Requirements)
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	var document catalog.TemplateDocument
	if err := json.Unmarshal(templateDefinition, &document); err != nil {
		return nil, err
	}
	requirements := make([]reportcore.Requirement, 0, len(document.Requirements))
	for _, value := range document.Requirements {
		requirements = append(requirements, reportcore.Requirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: value.Instructions})
	}
	return requirements, nil
}

func reportRequirementsFromCapture(raw []byte) ([]reportcore.Requirement, error) {
	var captured []capturecore.Requirement
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	requirements := make([]reportcore.Requirement, 0, len(captured))
	for _, value := range captured {
		requirements = append(requirements, reportcore.Requirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: value.Instructions})
	}
	return requirements, nil
}
