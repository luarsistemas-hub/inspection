package create_note_upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/features/transcription/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Command struct {
	TenantID, ResponsibilityID                   identity.ID
	ClientNoteID, RequirementKey                 string
	PhotoMediaID, VideoAttemptID                 *identity.ID
	StartMS, EndMS                               *int64
	TimeBasis, ConsentVersion, DisclosureVersion string
	ContentType, ContentSHA256                   string
	SizeBytes, DurationMS                        int64
}

type UploadIntent struct {
	NoteID      identity.ID
	URL         string
	UploadProof string
	ExpiresAt   time.Time
}

type Dependencies struct {
	DB     *gorm.DB
	Store  objectstore.Store
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
	Now    func() time.Time
}

// Setup validates scoped admission, reserves quota and creates one private multipart intent.
func Setup(ctx context.Context, d Dependencies, cmd Command) (UploadIntent, error) {
	if d.DB == nil || d.Store.Client == nil || cmd.TenantID == (identity.ID{}) || cmd.ResponsibilityID == (identity.ID{}) {
		return UploadIntent{}, apperror.New(apperror.InvalidInput, "input", "A solicitação de gravação é inválida.")
	}
	clientNote, err := identity.ParseID(cmd.ClientNoteID)
	if err != nil || cmd.RequirementKey == "" {
		return UploadIntent{}, apperror.New(apperror.InvalidInput, "clientNoteId", "A gravação informada é inválida.")
	}
	photoID, attemptID := "", ""
	if cmd.PhotoMediaID != nil {
		photoID = cmd.PhotoMediaID.String()
	}
	if cmd.VideoAttemptID != nil {
		attemptID = cmd.VideoAttemptID.String()
	}
	if err := core.ValidateAnchor(anchorKind(photoID, attemptID), photoID, attemptID, cmd.StartMS, cmd.EndMS, cmd.TimeBasis); err != nil {
		return UploadIntent{}, apperror.New(apperror.InvalidInput, "anchor", "Selecione uma foto ou um intervalo válido do vídeo.")
	}
	if err := core.ValidateAudio(cmd.ContentType, cmd.ContentSHA256, cmd.SizeBytes, cmd.DurationMS); err != nil {
		return UploadIntent{}, apperror.New(apperror.InvalidInput, "audio", "O áudio precisa estar em um formato e tamanho permitidos.")
	}
	if cmd.ConsentVersion == "" || cmd.DisclosureVersion == "" {
		return UploadIntent{}, apperror.New(apperror.ConsentRequired, "consent", "Confirme a autorização de uso de voz para continuar.")
	}
	now := time.Now().UTC()
	if d.Now != nil {
		now = d.Now().UTC()
	}
	digestPayload, _ := json.Marshal(cmd)
	payloadHash := sha256.Sum256(digestPayload)
	payloadDigest := hex.EncodeToString(payloadHash[:])
	within := d.Within
	if within == nil {
		within = (tenanttx.Runner{DB: d.DB}).Within
	}
	var previous struct {
		ID                  identity.ID
		PayloadDigest       string
		Status              string
		UploadKey, UploadID *string
	}
	if err := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		return tx.Raw("SELECT id, idempotency_payload_digest AS payload_digest, status, upload_key, upload_id FROM transcription.audio_notes WHERE tenant_id=? AND responsibility_id=? AND client_note_id=?", cmd.TenantID, cmd.ResponsibilityID, clientNote).Scan(&previous).Error
	}); err != nil {
		return UploadIntent{}, apperror.Wrap(apperror.Internal, err)
	}
	if previous.ID != (identity.ID{}) {
		if previous.PayloadDigest != payloadDigest {
			return UploadIntent{}, apperror.New(apperror.Conflict, "clientNoteId", "O identificador da gravação já foi utilizado com outros dados.")
		}
		if previous.Status == "CANCELED" || previous.Status == "EXPIRED" {
			return UploadIntent{}, apperror.New(apperror.InvalidState, "clientNoteId", "Esta gravação já foi encerrada e não pode ser retomada.")
		}
		if previous.UploadKey == nil || previous.UploadID == nil {
			return UploadIntent{}, apperror.New(apperror.InvalidState, "clientNoteId", "A gravação não pode ser retomada.")
		}
		part, err := d.Store.PresignPart(ctx, *previous.UploadKey, *previous.UploadID, 1, 10*time.Minute)
		if err != nil {
			return UploadIntent{}, apperror.Wrap(apperror.DependencyUnavailable, err)
		}
		return UploadIntent{NoteID: previous.ID, URL: part.URL, UploadProof: *previous.UploadID, ExpiresAt: part.ExpiresAt}, nil
	}
	// Use the stable note UUID as the opaque storage identity. The resulting key contains no user data.
	key, uploadID, err := d.Store.CreateMultipart(ctx, cmd.TenantID, clientNote, cmd.ContentType)
	if err != nil {
		return UploadIntent{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	cleanup := func() { _ = d.Store.Client.AbortMultipart(ctx, d.Store.Bucket, key, uploadID) }
	intent, err := d.Store.PresignPart(ctx, key, uploadID, 1, 10*time.Minute)
	if err != nil {
		cleanup()
		return UploadIntent{}, apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	var result UploadIntent
	err = within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var existing struct {
			ID                  identity.ID
			PayloadDigest       string
			UploadKey, UploadID *string
			Status              string
		}
		find := tx.Raw("SELECT id, idempotency_payload_digest AS payload_digest, upload_key, upload_id, status FROM transcription.audio_notes WHERE tenant_id=? AND inspection_id=(SELECT inspection_id FROM inspections.responsibilities WHERE tenant_id=? AND id=?) AND responsibility_id=? AND client_note_id=?", cmd.TenantID, cmd.TenantID, cmd.ResponsibilityID, cmd.ResponsibilityID, clientNote).Scan(&existing)
		if find.Error != nil {
			return find.Error
		}
		if existing.ID != (identity.ID{}) {
			if existing.PayloadDigest != payloadDigest {
				return apperror.New(apperror.Conflict, "clientNoteId", "O identificador da gravação já foi utilizado com outros dados.")
			}
			if existing.Status == "CANCELED" || existing.Status == "EXPIRED" {
				return apperror.New(apperror.InvalidState, "clientNoteId", "Esta gravação já foi encerrada e não pode ser retomada.")
			}
			return apperror.New(apperror.Conflict, "clientNoteId", "A gravação já foi iniciada. Retome o envio existente.")
		}
		// Writers with a video anchor lock video attempt → capture draft → tenant policy.
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=? AND status NOT IN ('SUBMITTED','EXPIRED','REVOKED','CANCELED')", cmd.TenantID, cmd.ResponsibilityID).First(&responsibility).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "responsibility", "A inspeção não está disponível.")
			}
			return err
		}
		eligible, err := environmentCaptureEligible(tx, cmd.TenantID, responsibility.InspectionID)
		if err != nil {
			return err
		}
		if !eligible {
			return apperror.New(apperror.InvalidState, "inspection", "Esta vistoria não é elegível para anotações de voz.")
		}
		var attempt database.VideoAttempt
		if attemptID != "" {
			attemptQuery := tx.Where("tenant_id=? AND responsibility_id=? AND id=?", cmd.TenantID, cmd.ResponsibilityID, cmd.VideoAttemptID)
			if tx.Dialector.Name() == "postgres" {
				attemptQuery = attemptQuery.Clauses(clause.Locking{Strength: "KEY SHARE"})
			}
			if err := attemptQuery.First(&attempt).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return apperror.New(apperror.NotFound, "videoAttemptId", "O vídeo selecionado não está disponível nesta captura.")
				}
				return err
			}
			if attempt.Status == "CANCELED" || attempt.Status == "REJECTED" || attempt.Status == "EXPIRED" {
				return apperror.New(apperror.InvalidState, "videoAttemptId", "Esta tentativa de vídeo foi encerrada e não pode receber uma gravação.")
			}
		}
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", cmd.TenantID, cmd.ResponsibilityID).First(&draft).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "inspection", "A captura não está mais aberta.")
			}
			return err
		}
		// Lock the tenant policy before counting so quota reservation is serialized.
		var policy struct {
			Enabled                                    bool
			TenantNoteLimit, InspectionNoteLimit       int
			TenantAudioLimitMS, InspectionAudioLimitMS int64
			Version                                    int
		}
		policyQuery := "SELECT enabled, tenant_note_limit, tenant_audio_limit_ms, inspection_note_limit, inspection_audio_limit_ms, version FROM transcription.tenant_policies WHERE tenant_id=?"
		if tx.Dialector.Name() == "postgres" {
			policyQuery += " FOR UPDATE"
		}
		if err := tx.Raw(policyQuery, cmd.TenantID).Scan(&policy).Error; err != nil {
			return err
		}
		if !policy.Enabled {
			return apperror.New(apperror.VoiceNotEnabled, "voicePolicy", "A gravação de voz não está habilitada para este cliente.")
		}
		var consent database.ProcessingAcceptance
		if err := tx.Where("tenant_id=? AND responsibility_id=?", cmd.TenantID, cmd.ResponsibilityID).First(&consent).Error; err != nil || consent.DisclosureVersion != cmd.DisclosureVersion || !consent.VoiceProcessing || consent.VoiceConsentVersion != cmd.ConsentVersion || !consent.AIAnalysis || (photoID != "" && !consent.PhotoProcessing) || (attemptID != "" && !consent.VideoProcessing) {
			return apperror.New(apperror.ConsentRequired, "consent", "A autorização atual para processamento de voz é necessária.")
		}
		if photoID != "" {
			var media database.MediaObject
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND id=? AND requirement_key=? AND media_kind='PHOTO' AND status='READY'", cmd.TenantID, cmd.ResponsibilityID, cmd.PhotoMediaID, cmd.RequirementKey).First(&media).Error; err != nil {
				return apperror.New(apperror.NotFound, "photoMediaId", "A foto selecionada não está disponível nesta captura.")
			}
		} else {
			if cmd.EndMS == nil || *cmd.EndMS > int64(attempt.MaxDurationSeconds)*1000 {
				return apperror.New(apperror.InvalidInput, "endMs", "O intervalo precisa estar dentro da duração do vídeo.")
			}
		}
		var tenantCount, inspectionCount int64
		var tenantAudio, inspectionAudio int64
		reservationTotals := `SELECT coalesce(sum(attempts),0),coalesce(sum(duration_ms*attempts),0) FROM (
	 SELECT n.duration_ms,CASE WHEN count(j.id)>1 THEN count(j.id) ELSE 1 END AS attempts FROM transcription.audio_notes n
	 LEFT JOIN transcription.jobs j ON j.tenant_id=n.tenant_id AND j.note_id=n.id
	 WHERE n.tenant_id=? AND (n.status='UPLOADING' OR j.id IS NOT NULL) %s GROUP BY n.tenant_id,n.id,n.duration_ms
	) reservations`
		if err := tx.Raw(fmt.Sprintf(reservationTotals, ""), cmd.TenantID).Row().Scan(&tenantCount, &tenantAudio); err != nil {
			return err
		}
		if err := tx.Raw(fmt.Sprintf(reservationTotals, "AND n.inspection_id=?"), cmd.TenantID, responsibility.InspectionID).Row().Scan(&inspectionCount, &inspectionAudio); err != nil {
			return err
		}
		p := core.Policy{Enabled: true, TenantNoteLimit: policy.TenantNoteLimit, TenantAudioLimitMS: policy.TenantAudioLimitMS, InspectionNoteLimit: policy.InspectionNoteLimit, InspectionAudioLimitMS: policy.InspectionAudioLimitMS}
		if err := p.CanReserve(int(tenantCount), tenantAudio, int(inspectionCount), inspectionAudio, cmd.DurationMS); err != nil {
			if errors.Is(err, core.ErrLimitReached) {
				return apperror.New(apperror.LimitReached, "audio", "O limite de uso de voz foi atingido.")
			}
			return apperror.New(apperror.InvalidInput, "limits", "Os limites de uso de voz precisam ser configurados.")
		}
		kind := anchorKind(photoID, attemptID)
		var sourceMedia *identity.ID
		if cmd.PhotoMediaID != nil {
			sourceMedia = cmd.PhotoMediaID
		}
		row := map[string]any{"tenant_id": cmd.TenantID, "id": clientNote, "inspection_id": responsibility.InspectionID, "responsibility_id": cmd.ResponsibilityID, "client_note_id": clientNote, "requirement_key": cmd.RequirementKey, "consent_version": cmd.ConsentVersion, "disclosure_version": cmd.DisclosureVersion, "target_kind": kind, "photo_media_id": cmd.PhotoMediaID, "video_attempt_id": cmd.VideoAttemptID, "source_media_id": sourceMedia, "start_ms": cmd.StartMS, "end_ms": cmd.EndMS, "time_basis": nil, "duration_ms": cmd.DurationMS, "content_sha256": cmd.ContentSHA256, "content_type": cmd.ContentType, "size_bytes": cmd.SizeBytes, "status": "UPLOADING", "anchor_status": "PENDING", "version": 1, "audio_deletion_status": "PENDING", "upload_key": key, "upload_id": uploadID, "created_at": now, "updated_at": now, "idempotency_payload_digest": payloadDigest}
		if attemptID != "" {
			row["time_basis"] = cmd.TimeBasis
		}
		if err := tx.Table("transcription.audio_notes").Create(&row).Error; err != nil {
			return err
		}
		result = UploadIntent{NoteID: clientNote, URL: intent.URL, UploadProof: uploadID, ExpiresAt: intent.ExpiresAt}
		return nil
	})
	if err != nil {
		cleanup()
		var raced struct {
			ID            identity.ID
			PayloadDigest string
			Status        string
			UploadKey     *string
			UploadID      *string
		}
		lookupErr := within(ctx, cmd.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT id, idempotency_payload_digest AS payload_digest, status, upload_key, upload_id FROM transcription.audio_notes WHERE tenant_id=? AND responsibility_id=? AND client_note_id=?", cmd.TenantID, cmd.ResponsibilityID, clientNote).Scan(&raced).Error
		})
		if lookupErr == nil && raced.ID != (identity.ID{}) {
			if raced.PayloadDigest != payloadDigest {
				return UploadIntent{}, apperror.New(apperror.Conflict, "clientNoteId", "O identificador da gravação já foi utilizado com outros dados.")
			}
			if raced.Status == "CANCELED" || raced.Status == "EXPIRED" {
				return UploadIntent{}, apperror.New(apperror.InvalidState, "clientNoteId", "Esta gravação já foi encerrada e não pode ser retomada.")
			}
			if raced.UploadKey == nil || raced.UploadID == nil {
				return UploadIntent{}, apperror.New(apperror.InvalidState, "clientNoteId", "A gravação não pode ser retomada.")
			}
			racedPart, partErr := d.Store.PresignPart(ctx, *raced.UploadKey, *raced.UploadID, 1, 10*time.Minute)
			if partErr != nil {
				return UploadIntent{}, apperror.Wrap(apperror.DependencyUnavailable, partErr)
			}
			return UploadIntent{NoteID: raced.ID, URL: racedPart.URL, UploadProof: *raced.UploadID, ExpiresAt: racedPart.ExpiresAt}, nil
		}
		return UploadIntent{}, err
	}
	if result.URL == "" {
		result = UploadIntent{NoteID: clientNote, URL: intent.URL, UploadProof: uploadID, ExpiresAt: intent.ExpiresAt}
	}
	return result, nil
}

func environmentCaptureEligible(tx *gorm.DB, tenantID, inspectionID identity.ID) (bool, error) {
	var inspection database.Inspection
	if err := tx.Select("template_version_id").Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	var version database.TemplateVersion
	if err := tx.Select("schema_version", "definition_json").Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	var definition struct {
		CaptureLayout string `json:"captureLayout"`
	}
	if err := json.Unmarshal(version.DefinitionJSON, &definition); err != nil {
		return false, nil
	}
	return version.SchemaVersion >= catalog.EnvironmentSchemaVersion && definition.CaptureLayout == "ENVIRONMENTS", nil
}

func anchorKind(photoID, attemptID string) string {
	if photoID != "" {
		return "PHOTO"
	}
	if attemptID != "" {
		return "VIDEO_INTERVAL"
	}
	return ""
}
