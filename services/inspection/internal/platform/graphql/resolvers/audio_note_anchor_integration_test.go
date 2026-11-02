//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestAudioNoteVideoAnchorIT140IT141IT142IT143IT144IT145IT147IT149IT150IT151IT152IT239IT240(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedVideoOriginFixture(ctx, harness.VideoOriginFixtureOptions{Active: true, LinkVideoAttempt: true})
	if err != nil {
		t.Fatal(err)
	}
	var inspectionID identity.ID
	var frame database.VideoFrame
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		inspectionID = responsibility.InspectionID
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.FrameID).Take(&frame).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	actor := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	queryCtx := requestctx.WithMetadata(ctx, actor)
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: mediator.New(), Store: h.Store, Authorizer: auth.Authorizer{}}}))
	anchorID := identity.NewID()
	pendingID, invalidID := identity.NewID(), identity.NewID()
	// The voice note may be created while its video attempt has no media yet;
	// the fixture links its prepared media to that attempt to verify read-time resolution.
	attemptID := *f.VideoAttemptID
	now := time.Now().UTC().Truncate(time.Microsecond)
	startMS, endMS := frame.PTSMicros/1000, frame.PTSMicros/1000+1000
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		for _, n := range []struct {
			id     identity.ID
			status string
		}{{anchorID, "VALID"}, {pendingID, "PENDING"}, {invalidID, "INVALID"}} {
			var sourceMediaID any = f.MediaID
			if n.status == "PENDING" {
				// This note arrived before its video source was received/validated.
				sourceMediaID = nil
			}
			if err := tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,video_attempt_id,source_media_id,start_ms,end_ms,time_basis,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,no_speech,created_at,updated_at)
VALUES (?,?,?,?,?,'video:main','v1','v1','VIDEO_INTERVAL',?,?,?,?,'CAPTURE_CLOCK',1000,?,'audio/webm',1000,'COMPLETED',?,false,?,?)`, f.TenantID, n.id, inspectionID, f.ResponsibilityID, n.id, attemptID, sourceMediaID, startMS, endMS, fmt.Sprintf("%064x", 1), n.status, now, now).Error; err != nil {
				return err
			}
			revisionID := identity.NewID()
			if err := tx.Create(&database.TranscriptRevision{ID: revisionID, TenantID: f.TenantID, NoteID: n.id, RevisionNumber: 1, Text: "cinco pratos", Kind: "PARTICIPANT_DECLARATION", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Table("transcription.audio_notes").Where("tenant_id=? AND id=?", f.TenantID, n.id).Update("current_revision_id", revisionID).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func(id identity.ID) map[string]any {
		t.Helper()
		query := fmt.Sprintf(`query { audioNote(id:"%s") { status startMs endMs sourceMediaId anchor { kind status mediaId mediaStatus approximate previewUrl playbackUrl frames { id timestampMs url status } framesTruncated visualCoverage } revisions { text } } }`, id)
		var payload struct {
			Data struct {
				Note map[string]any `json:"audioNote"`
			} `json:"data"`
			Errors []struct{ Message string } `json:"errors"`
		}
		if err := json.Unmarshal([]byte(executeTask06GraphQL(t, server, query, queryCtx)), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Errors) != 0 || payload.Data.Note == nil {
			t.Fatalf("audioNote query errors=%+v data=%+v", payload.Errors, payload.Data.Note)
		}
		return payload.Data.Note
	}
	first := read(anchorID)
	anchor := first["anchor"].(map[string]any)
	if anchor["kind"] != "VIDEO_INTERVAL" || anchor["status"] != "VALID" || anchor["approximate"] != true || anchor["mediaId"] != f.MediaID.String() || anchor["playbackUrl"] == nil {
		t.Fatalf("IT-140 playback/approximate anchor = %+v", anchor)
	}
	frames := anchor["frames"].([]any)
	if len(frames) != 1 {
		t.Fatalf("IT-141 frame is context only and should be projected once: %+v", anchor)
	}
	frameProjection := frames[0].(map[string]any)
	if frameProjection["status"] != "AVAILABLE" || frameProjection["url"] == nil || anchor["visualCoverage"] != "FRAMES" {
		t.Fatalf("IT-141 available context frame = %+v", frameProjection)
	}
	if url, _ := anchor["playbackUrl"].(string); url == "" || fmt.Sprint(frameProjection["url"]) == "" {
		t.Fatalf("IT-140/IT-141 missing authenticated relative URLs: %+v", anchor)
	}
	// IT-145 bounds a dense interval to six previews and explicitly reports truncation.
	reader, err := h.Store.Client.Get(ctx, h.Store.Bucket, frame.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	frameBytes, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		for i := 1; i <= 7; i++ {
			copyFrame := frame
			copyFrame.ID = identity.NewID()
			copyFrame.PTSMicros = frame.PTSMicros + int64(i*1000)
			copyFrame.ObjectKey = "tenant/" + f.TenantID.String() + "/video-frames/" + copyFrame.ID.String() + ".jpg"
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, copyFrame.ObjectKey, bytes.NewReader(frameBytes), int64(len(frameBytes)), "image/png"); err != nil {
				return err
			}
			if err := tx.Create(&copyFrame).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	truncated := read(anchorID)["anchor"].(map[string]any)
	if truncated["framesTruncated"] != true || len(truncated["frames"].([]any)) != 6 {
		t.Fatalf("IT-145 dense interval projection = %+v", truncated)
	}

	// IT-144 keeps the interval readable when no prepared frame falls near it.
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Table("transcription.audio_notes").Where("tenant_id=? AND id=?", f.TenantID, anchorID).Updates(map[string]any{"start_ms": int(startMS) + 60_000, "end_ms": int(endMS) + 60_000}).Error
	}); err != nil {
		t.Fatal(err)
	}
	limited := read(anchorID)["anchor"].(map[string]any)
	if limited["visualCoverage"] != "LIMITED" || len(limited["frames"].([]any)) != 0 {
		t.Fatalf("IT-144 interval without nearby frame = %+v", limited)
	}

	// A note without its source_media_id resolves the uploaded media from its video attempt.
	invalid := read(invalidID)["anchor"].(map[string]any)
	pending := read(pendingID)["anchor"].(map[string]any)
	pendingNote := read(pendingID)
	if invalid["status"] != "INVALID" || invalid["playbackUrl"] != nil || pending["status"] != "VALID" || pending["mediaId"] != f.MediaID.String() || pendingNote["sourceMediaId"] != f.MediaID.String() {
		t.Fatalf("IT-143/IT-150 invalid and late-linked anchors = invalid:%+v pending:%+v", invalid, pending)
	}

	// IT-147 has no READY preparation yet, so the note stays visible with pending context.
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", f.TenantID, f.PreparationID).Update("status", "PROCESSING").Error
	}); err != nil {
		t.Fatal(err)
	}
	preparing := read(anchorID)["anchor"].(map[string]any)
	if preparing["status"] != "VALID" || preparing["mediaStatus"] != "PREPARING" || len(preparing["frames"].([]any)) != 0 {
		t.Fatalf("IT-147 preparation in progress should retain note and show no stale frame: %+v", preparing)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", f.TenantID, f.PreparationID).Update("status", "READY").Error
	}); err != nil {
		t.Fatal(err)
	}
	if repeated := read(anchorID)["anchor"].(map[string]any); repeated["mediaId"] != f.MediaID.String() {
		t.Fatalf("IT-149 historical anchor moved from source media: %+v", repeated)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Table("transcription.audio_notes").Where("tenant_id=? AND id=?", f.TenantID, anchorID).Updates(map[string]any{"start_ms": startMS, "end_ms": endMS}).Error
	}); err != nil {
		t.Fatal(err)
	}

	// IT-142/IT-239/IT-240: original playback expires while retained frame context remains.
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", f.TenantID, f.MediaID).Updates(map[string]any{"status": "EXPIRED", "original_deleted_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	expired := read(anchorID)["anchor"].(map[string]any)
	if expired["mediaStatus"] != "UNAVAILABLE" || expired["playbackUrl"] != nil || len(expired["frames"].([]any)) == 0 || expired["visualCoverage"] != "FRAMES" {
		t.Fatalf("IT-142/IT-239/IT-240 expired original with retained frame = %+v", expired)
	}
	deleter, ok := h.Store.Client.(objectstore.Deleter)
	if !ok {
		t.Fatal("test object store does not support deletion")
	}
	if err := deleter.Delete(ctx, h.Store.Bucket, frame.ObjectKey); err != nil {
		t.Fatal(err)
	}
	withExpiredFrame := read(anchorID)["anchor"].(map[string]any)
	foundExpired := false
	for _, candidate := range withExpiredFrame["frames"].([]any) {
		candidate := candidate.(map[string]any)
		if candidate["id"] == frame.ID.String() {
			foundExpired = candidate["status"] == "EXPIRED" && candidate["url"] == nil
		}
	}
	if !foundExpired {
		t.Fatalf("IT-142/IT-240 unretained frame must be EXPIRED with no URL: %+v", withExpiredFrame)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Update("status", "PURGED").Error
	}); err != nil {
		t.Fatal(err)
	}
	purged := read(anchorID)["anchor"].(map[string]any)
	if purged["mediaStatus"] != "PURGED" || purged["mediaId"] != f.MediaID.String() || purged["playbackUrl"] != nil {
		t.Fatalf("IT-151 purged source must remain attached to its historical media id: %+v", purged)
	}
	listQuery := fmt.Sprintf(`query { audioNotes(inspectionId:"%s",environmentId:"video:main",first:25) { nodes { id sourceMediaId startMs endMs } } }`, inspectionID)
	anchorReadQueries := map[string]int{}
	if err := h.DB.Callback().Query().After("gorm:query").Register("r1be:audio_anchor_batch_count", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		for _, table := range []string{"media_objects", "video_sources", "derivatives", "video_preparation_jobs", "video_frames"} {
			if strings.Contains(sql, table) {
				anchorReadQueries[table]++
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.DB.Callback().Query().Remove("r1be:audio_anchor_batch_count") })
	var list struct {
		Data struct {
			Notes struct {
				Nodes []map[string]any `json:"nodes"`
			} `json:"audioNotes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(executeTask06GraphQL(t, server, listQuery, queryCtx)), &list); err != nil || len(list.Data.Notes.Nodes) != 3 {
		t.Fatalf("IT-152 notes sharing a clip remain separately navigable: nodes=%d err=%v", len(list.Data.Notes.Nodes), err)
	}
	for table, queries := range anchorReadQueries {
		if queries > 1 {
			t.Errorf("audio anchor media enrichment issued %d reads of %s for one page; want one batch", queries, table)
		}
	}
	if read(anchorID)["sourceMediaId"] != f.MediaID.String() || len(read(anchorID)["revisions"].([]any)) != 1 {
		t.Fatalf("IT-152 source citations must remain independent")
	}
}
