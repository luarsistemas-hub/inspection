//go:build integration

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/transcription/create_note_upload"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestVoiceMediaLifecycleIT237IT243IT249IT311(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := NewPostgresFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()

	t.Run("IT-237 clip attempts retain independent timelines", func(t *testing.T) {
		f := seedEligibleVoiceFixture(t, ctx, h)
		first, err := create_note_upload.Setup(ctx, lifecycleUploadDeps(h), lifecycleCommand(f, identity.NewID(), f.VideoAttemptID, 500, 1500))
		if err != nil {
			t.Fatal(err)
		}
		secondAttempt := identity.NewID()
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", f.TenantID, f.VideoAttemptID).Update("status", "CANCELED").Error; err != nil {
				return err
			}
			return tx.Create(&database.VideoAttempt{ID: secondAttempt, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, Kind: "CURRENT", RequirementKey: strPtr(voiceRequirementKey(f)), PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: identity.NewID().String(), PayloadDigest: strings.Repeat("b", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		second, err := create_note_upload.Setup(ctx, lifecycleUploadDeps(h), lifecycleCommand(f, identity.NewID(), secondAttempt, 2000, 3200))
		if err != nil {
			t.Fatal(err)
		}
		var rows []struct {
			ID             identity.ID
			VideoAttemptID identity.ID
			StartMS, EndMS int64
		}
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT id,video_attempt_id,start_ms,end_ms FROM transcription.audio_notes WHERE tenant_id=? AND id IN ? ORDER BY id", f.TenantID, []identity.ID{first.NoteID, second.NoteID}).Scan(&rows).Error
		}); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].VideoAttemptID == rows[1].VideoAttemptID || rows[0].VideoAttemptID == (identity.ID{}) || rows[1].VideoAttemptID == (identity.ID{}) {
			t.Fatalf("notes did not retain distinct clip attempts: %+v", rows)
		}
		if rows[0].StartMS == rows[1].StartMS && rows[0].EndMS == rows[1].EndMS {
			t.Fatalf("clip timelines were merged: %+v", rows)
		}
	})

	t.Run("IT-243 final prepared duration rejects out-of-range interval", func(t *testing.T) {
		f := seedEligibleVoiceFixture(t, ctx, h)
		intent, err := create_note_upload.Setup(ctx, lifecycleUploadDeps(h), lifecycleCommand(f, identity.NewID(), f.VideoAttemptID, 1500, 2501))
		if err != nil {
			t.Fatal(err)
		}
		revision := identity.NewID()
		if err := prepareLifecycleClip(ctx, h, f, intent.NoteID, revision, 2500, "interval beyond clip"); err != nil {
			t.Fatal(err)
		}
		var status string
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := catalog_notes.ResolveAnchors(ctx, tx, catalog_notes.Room{TenantID: f.TenantID, InspectionID: f.InspectionID, ResponsibilityID: f.ResponsibilityID, RequirementKey: voiceRequirementKey(f), MediaIDs: []identity.ID{f.MediaID}}, time.Now().UTC()); err != nil {
				return err
			}
			return tx.Raw("SELECT anchor_status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", f.TenantID, intent.NoteID).Row().Scan(&status)
		}); err != nil {
			t.Fatal(err)
		}
		if status != "INVALID" {
			t.Fatalf("anchor_status=%s, want INVALID for endMs beyond final duration", status)
		}
		var outcome catalog_notes.Outcome
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			var evalErr error
			outcome, evalErr = catalog_notes.Evaluate(ctx, tx, catalog_notes.Room{TenantID: f.TenantID, InspectionID: f.InspectionID, ResponsibilityID: f.ResponsibilityID, RequirementKey: voiceRequirementKey(f), MediaIDs: []identity.ID{f.MediaID}, Generation: 7}, time.Now().UTC(), true)
			return evalErr
		}); err != nil {
			t.Fatal(err)
		}
		if outcome.Manifest == nil || len(outcome.Manifest.Included()) != 0 || len(outcome.Manifest.Entries) != 1 || outcome.Manifest.Entries[0].Reason != catalog_notes.ReasonAnchorInvalid {
			t.Fatalf("invalid interval entered catalog manifest: %+v", outcome.Manifest)
		}
	})

	t.Run("IT-249 removed room or source invalidates active note and preserves citation", func(t *testing.T) {
		for _, scenario := range []struct {
			name       string
			removeRoom bool
		}{
			{name: "room removal", removeRoom: true},
			{name: "source removal"},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				f := seedEligibleVoiceFixture(t, ctx, h)
				intent, err := create_note_upload.Setup(ctx, lifecycleUploadDeps(h), lifecycleCommand(f, identity.NewID(), f.VideoAttemptID, 500, 1500))
				if err != nil {
					t.Fatal(err)
				}
				revision := identity.NewID()
				if err := prepareLifecycleClip(ctx, h, f, intent.NoteID, revision, 2000, "historical text citation"); err != nil {
					t.Fatal(err)
				}
				room := catalog_notes.Room{TenantID: f.TenantID, InspectionID: f.InspectionID, ResponsibilityID: f.ResponsibilityID, RequirementKey: voiceRequirementKey(f), MediaIDs: []identity.ID{f.MediaID}, Generation: 8}
				var historical catalog_notes.Outcome
				if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
					var evalErr error
					historical, evalErr = catalog_notes.Evaluate(ctx, tx, room, time.Now().UTC(), true)
					return evalErr
				}); err != nil {
					t.Fatal(err)
				}
				if historical.Manifest == nil || len(historical.Manifest.Included()) != 1 || historical.Manifest.Included()[0].RevisionID == nil || *historical.Manifest.Included()[0].RevisionID != revision {
					t.Fatalf("initial catalog did not fix the exact note revision: %+v", historical.Manifest)
				}
				var current catalog_notes.Outcome
				if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
					if scenario.removeRoom {
						// No current media is a removed environment's room scope.
					} else if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Update("status", "PURGED").Error; err != nil {
						return err
					}
					currentRoom := catalog_notes.Room{TenantID: f.TenantID, InspectionID: f.InspectionID, ResponsibilityID: f.ResponsibilityID, RequirementKey: voiceRequirementKey(f), Generation: 9}
					if !scenario.removeRoom {
						currentRoom.MediaIDs = []identity.ID{f.MediaID}
					}
					var evalErr error
					current, evalErr = catalog_notes.Evaluate(ctx, tx, currentRoom, time.Now().UTC(), true)
					return evalErr
				}); err != nil {
					t.Fatal(err)
				}
				var state struct {
					AnchorStatus  string
					SourceMediaID identity.ID
				}
				if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
					if err := tx.Raw("SELECT anchor_status,source_media_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?", f.TenantID, intent.NoteID).Scan(&state).Error; err != nil {
						return err
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if state.SourceMediaID != f.MediaID {
					t.Fatalf("historical anchor identity was lost: %+v", state)
				}
				if !scenario.removeRoom && state.AnchorStatus != "INVALID" {
					t.Fatalf("removed source left active anchor status %s", state.AnchorStatus)
				}
				if current.Manifest == nil || len(current.Manifest.Included()) != 0 || len(current.Manifest.Entries) != 1 || current.Manifest.Entries[0].State != catalog_notes.StateExcluded {
					t.Fatalf("removed room/source remained eligible for a new suggestion: %+v", current.Manifest)
				}
				if historical.Manifest.Entries[0].RevisionID == nil || *historical.Manifest.Entries[0].RevisionID != revision {
					t.Fatalf("published historical citation changed after media removal: %+v", historical.Manifest.Entries)
				}
			})
		}
	})

	t.Run("IT-311 legacy inspection receives eligibility error", func(t *testing.T) {
		f, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{MediaKind: "VIDEO", RequirementKey: "room-1", SchemaVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", f.TenantID, f.ResponsibilityID).Update("status", "PENDING").Error; err != nil {
				return err
			}
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, f.DraftID).Update("status", "OPEN").Error; err != nil {
				return err
			}
			return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", f.TenantID, f.VideoAttemptID).Update("status", "ACTIVE").Error
		}); err != nil {
			t.Fatal(err)
		}
		_, err = create_note_upload.Setup(ctx, lifecycleUploadDeps(h), lifecycleCommand(f, identity.NewID(), f.VideoAttemptID, 500, 1500))
		if !apperror.Is(err, apperror.InvalidState) || !strings.Contains(err.Error(), "não é elegível") {
			t.Fatalf("legacy voice admission error=%v; want Portuguese eligibility error", err)
		}
	})
}

func seedEligibleVoiceFixture(t *testing.T, ctx context.Context, h *Harness) Task06Fixture {
	t.Helper()
	f, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{MediaKind: "VIDEO", SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", f.TenantID, f.ResponsibilityID).Update("status", "PENDING").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, f.DraftID).Update("status", "OPEN").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", f.TenantID, f.VideoAttemptID).Update("status", "ACTIVE").Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO transcription.tenant_policies(tenant_id,enabled,tenant_note_limit,tenant_audio_limit_ms,inspection_note_limit,inspection_audio_limit_ms,version,created_at,updated_at) VALUES(?,true,20,600000,10,60000,1,?,?)`, f.TenantID, now, now).Error; err != nil {
			return err
		}
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, VideoProcessing: true, VoiceProcessing: true, VoiceConsentVersion: "voice-v1", AIAnalysis: true, AcceptedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func lifecycleUploadDeps(h *Harness) create_note_upload.Dependencies {
	return create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}
}

func lifecycleCommand(f Task06Fixture, clientID, attemptID identity.ID, start, end int64) create_note_upload.Command {
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	return create_note_upload.Command{TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ClientNoteID: clientID.String(), RequirementKey: voiceRequirementKey(f), VideoAttemptID: &attemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK", ConsentVersion: "voice-v1", DisclosureVersion: "pt-BR-1", ContentType: "audio/webm", ContentSHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(data)), DurationMS: 1000}
}

func prepareLifecycleClip(ctx context.Context, h *Harness, f Task06Fixture, noteID, revisionID identity.ID, durationMS int64, text string) error {
	now := time.Now().UTC()
	return h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Update("status", "READY").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: identity.NewID(), TenantID: f.TenantID, MediaID: f.MediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.TenantID, MediaID: f.MediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: durationMS * 1000, Width: 640, Height: 480, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TranscriptRevision{ID: revisionID, TenantID: f.TenantID, NoteID: noteID, RevisionNumber: 1, Text: text, Kind: "PARTICIPANT_DECLARATION", AuthorID: &f.MembershipID, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE transcription.audio_notes SET status='COMPLETED',current_revision_id=?,no_speech=false WHERE tenant_id=? AND id=?", revisionID, f.TenantID, noteID).Error
	})
}

func strPtr(value string) *string { return &value }
