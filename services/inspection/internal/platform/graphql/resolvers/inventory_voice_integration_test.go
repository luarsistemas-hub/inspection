//go:build integration

package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

// voiceDraft is an inventory draft of one captured room that was cataloged
// once, with a voice-declared pending item and two transcript revisions.
type voiceDraft struct {
	version, environment, item identity.ID
	noteA, revisionA           identity.ID
	noteB, revisionB           identity.ID
	originRevision             int
}

func seedVoiceDraft(t *testing.T, role string) (api06Fixture, voiceDraft) {
	t.Helper()
	key := "env:new:" + identity.NewID().String()
	fx := newInventoryITFixtureWithOptions(t, role, harness.Task06FixtureOptions{InspectionStatus: "SUBMITTED", SchemaVersion: 2, RequirementKey: key, ComparisonMode: "CURRENT_ONLY"})
	d := voiceDraft{version: identity.NewID(), environment: identity.NewID(), item: identity.NewID(), noteA: identity.NewID(), revisionA: identity.NewID(), noteB: identity.NewID(), revisionB: identity.NewID()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND asset_id=?", fx.seed.TenantID, fx.seed.AssetID).Take(&origin).Error; err != nil {
			return err
		}
		d.originRevision = int(origin.Version)
		state, source := "READY", fx.seed.InspectionID
		if err := tx.Create(&database.OriginVersion{ID: d.version, TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: 2, Source: "CAPTURE", SourceInspectionID: &source, HasInventory: true, Status: "DRAFT", DraftState: &state, IdempotencyKey: "voice-draft-" + d.version.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		identityID := identity.NewID()
		if err := tx.Create(&database.InventoryEnvironment{ID: d.environment, TenantID: fx.seed.TenantID, OriginVersionID: d.version, IdentityID: identityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, RequirementKey: &key, Decision: "PENDING", Position: 0, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		confidence := 9000
		if err := tx.Create(&database.InventoryItem{ID: d.item, TenantID: fx.seed.TenantID, OriginVersionID: d.version, EnvironmentID: d.environment, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Pratos", Quantity: intPtr(5), Condition: "NOT_ASSESSABLE", Observation: "NOT_ASSESSABLE", ConfidenceBps: &confidence, Origin: "AI", Declared: true, Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		snapshot := fx.seed.PromptSnapshotID
		if err := tx.Create(&database.InventoryJob{ID: identity.NewID(), TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, OriginVersionID: d.version, Kind: "CATALOG", RequirementKey: key, Generation: 1, Status: "SUCCEEDED", NextAttemptAt: now, EnvironmentIdentityID: &identityID, PromptSnapshotID: &snapshot, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		for _, n := range []struct{ note, revision identity.ID }{{d.noteA, d.revisionA}, {d.noteB, d.revisionB}} {
			if err := tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,photo_media_id,source_media_id,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,no_speech,created_at,updated_at)
VALUES (?,?,?,?,?,?,'v1','v1','PHOTO',?,?,3000,?,'audio/webm',1000,'COMPLETED','VALID',false,?,?)`, fx.seed.TenantID, n.note, fx.seed.InspectionID, fx.seed.ResponsibilityID, n.note, key, fx.seed.MediaID, fx.seed.MediaID, strings.Repeat("a", 64), now, now).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.TranscriptRevision{ID: n.revision, TenantID: fx.seed.TenantID, NoteID: n.note, RevisionNumber: 1, Text: "cinco pratos", Kind: "PARTICIPANT_DECLARATION", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", n.revision, fx.seed.TenantID, n.note).Error; err != nil {
				return err
			}
		}
		for _, attribute := range []string{"name", "quantity"} {
			note, revision := d.noteA, d.revisionA
			if err := tx.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, ItemID: d.item, OriginVersionID: d.version, AttributeName: attribute, SourceKind: "VOICE", NoteID: &note, RevisionID: &revision, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return withScopeAuthorizer(fx), d
}

// withScopeAuthorizer serves the fixture with the production authorizer shape:
// grants resolve through the scope resolver, so new-work checks are enforced.
func withScopeAuthorizer(fx api06Fixture) api06Fixture {
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: fx.bus, Store: fx.h.Store, Authorizer: auth.Authorizer{Scopes: auth.GORMScopeResolver{DB: fx.h.DB}}}}))
	server.SetErrorPresenter(graph.PresentError)
	fx.server = server
	return fx
}

type userErrors struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type regenerationResponse struct {
	Data struct {
		Regenerate struct {
			JobID            *string      `json:"jobId"`
			Status           *string      `json:"status"`
			UserErrors       []userErrors `json:"userErrors"`
			ClientMutationID string       `json:"clientMutationId"`
		} `json:"regenerateInventorySuggestions"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func regenerate(t *testing.T, fx api06Fixture, d voiceDraft, version, environment string, expected int, revisions string, mutation string) regenerationResponse {
	t.Helper()
	query := fmt.Sprintf(`mutation { regenerateInventorySuggestions(input:{originVersionId:"%s",environmentId:"%s",expectedVersion:%d,revisionIds:%s,clientMutationId:"%s"}) { jobId status userErrors { code field message } clientMutationId } }`, version, environment, expected, revisions, mutation)
	var out regenerationResponse
	if err := json.Unmarshal([]byte(executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRegenerateInventorySuggestionsTransportIT368ToIT372(t *testing.T) {
	t.Run("IT-368 an authorized request schedules a run and echoes the mutation id", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		revisions := fmt.Sprintf(`["%s","%s"]`, d.revisionA, d.revisionB)
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, revisions, "regen-ok")
		r := out.Data.Regenerate
		if len(out.Errors) != 0 || len(r.UserErrors) != 0 || r.JobID == nil || r.Status == nil || *r.Status != "PENDING" || r.ClientMutationID != "regen-ok" {
			t.Fatalf("response = %+v", out)
		}
		var job database.InventoryJob
		if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, *r.JobID).Take(&job).Error; err != nil || job.Generation != 2 || job.Kind != "CATALOG" || job.OriginVersionID != d.version {
			t.Fatalf("job = %+v err=%v", job, err)
		}
		again := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, revisions, "regen-ok")
		if again.Data.Regenerate.JobID == nil || *again.Data.Regenerate.JobID != *r.JobID || len(again.Data.Regenerate.UserErrors) != 0 {
			t.Fatalf("a repeated mutation id must answer with the same run: %+v", again)
		}
	})
	t.Run("IT-369 malformed input is rejected in Portuguese without side effects", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		for name, tc := range map[string]struct{ version, environment, revisions string }{
			"empty selection":   {d.version.String(), d.environment.String(), `[]`},
			"malformed draft":   {"not-a-uuid", d.environment.String(), fmt.Sprintf(`["%s"]`, d.revisionA)},
			"malformed room":    {d.version.String(), "not-a-uuid", fmt.Sprintf(`["%s"]`, d.revisionA)},
			"malformed note id": {d.version.String(), d.environment.String(), `["x"]`},
		} {
			out := regenerate(t, fx, d, tc.version, tc.environment, d.originRevision, tc.revisions, "regen-bad-"+strings.ReplaceAll(name, " ", "-"))
			errs := out.Data.Regenerate.UserErrors
			if len(errs) != 1 || errs[0].Code != "INVALID_INPUT" || errs[0].Message == "" || out.Data.Regenerate.JobID != nil {
				t.Fatalf("%s: response = %+v", name, out)
			}
			if strings.Contains(errs[0].Message, "invalid") || strings.Contains(errs[0].Message, "required") {
				t.Fatalf("%s: the message is not Portuguese: %q", name, errs[0].Message)
			}
		}
		var jobs int64
		fx.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND generation>1", fx.seed.TenantID, d.version).Count(&jobs)
		if jobs != 0 {
			t.Fatalf("invalid requests scheduled %d runs", jobs)
		}
	})
	t.Run("IT-370 a stale origin revision is a conflict", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision+1, fmt.Sprintf(`["%s"]`, d.revisionB), "regen-stale")
		if errs := out.Data.Regenerate.UserErrors; len(errs) != 1 || errs[0].Code != "CONFLICT" || errs[0].Field != "expectedVersion" || out.Data.Regenerate.JobID != nil {
			t.Fatalf("response = %+v", out)
		}
	})
	t.Run("IT-371 a discarded draft is an invalid state", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, d.version).Update("status", "DISCARDED").Error; err != nil {
			t.Fatal(err)
		}
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, fmt.Sprintf(`["%s"]`, d.revisionB), "regen-discarded")
		if errs := out.Data.Regenerate.UserErrors; len(errs) != 1 || errs[0].Code != "INVALID_STATE" || errs[0].Message == "" {
			t.Fatalf("response = %+v", out)
		}
	})
	t.Run("IT-372 IT-230 an actor without the capability is denied and another tenant sees nothing", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Viewer)
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, fmt.Sprintf(`["%s"]`, d.revisionB), "regen-viewer")
		if errs := out.Data.Regenerate.UserErrors; len(errs) != 1 || errs[0].Code != "FORBIDDEN" || out.Data.Regenerate.JobID != nil {
			t.Fatalf("viewer response = %+v", out)
		}
		tenantB := identity.NewID()
		other := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantB, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: fx.seed.MembershipID, TenantID: tenantB, Roles: []string{auth.Employee}, ProductEntitlements: auth.ProductsForRole(auth.Employee)}})
		query := fmt.Sprintf(`mutation { regenerateInventorySuggestions(input:{originVersionId:"%s",environmentId:"%s",expectedVersion:%d,revisionIds:["%s"],clientMutationId:"regen-tenant-b"}) { jobId userErrors { code } } }`, d.version, d.environment, d.originRevision, d.revisionB)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, other, "", "")
		if !strings.Contains(response, `"code":"NOT_FOUND"`) && !strings.Contains(response, `"code":"FORBIDDEN"`) {
			t.Fatalf("another tenant's actor response = %s", response)
		}
		var jobs int64
		fx.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND generation>1", fx.seed.TenantID, d.version).Count(&jobs)
		if jobs != 0 {
			t.Fatalf("a denied request scheduled %d runs", jobs)
		}
	})
}

func TestInventoryItemProjectsAttributeSourcesIT126IT136IT167ToIT178IT206(t *testing.T) {
	fx, d := seedVoiceDraft(t, auth.Employee)
	currentRevision := identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.MembershipID).Update("name", "Revisora QA").Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Create(&database.TranscriptRevision{ID: currentRevision, TenantID: fx.seed.TenantID, NoteID: d.noteA, RevisionNumber: 2, Text: "seis pratos", Kind: "HUMAN_EDIT", AuthorID: &fx.seed.MembershipID, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Table("transcription.audio_notes").Where("tenant_id=? AND id=?", fx.seed.TenantID, d.noteA).Update("current_revision_id", currentRevision).Error
	}); err != nil {
		t.Fatal(err)
	}
	draftQuery := fmt.Sprintf(`query { inventoryDraft(originVersionId:"%s") { sourceInspectionId voiceSummary { pendingVoiceItems conflictItems staleSuggestions notesWithoutItem } environments { voiceSummary { pendingVoiceItems conflictItems staleSuggestions notesWithoutItem } items { id decision declared voiceConflicts { attributeName visualValue voiceValue } attributeSources { id attributeName sourceKind noteId revisionId currentRevisionId stale visualEvidenceId reviewedBy createdAt } } } } }`, d.version)
	type source struct {
		AttributeName, SourceKind                                           string
		NoteID, RevisionID, CurrentRevisionID, VisualEvidenceID, ReviewedBy *string
		Stale                                                               bool
	}
	read := func() []source {
		t.Helper()
		var out struct {
			Data struct {
				Draft struct {
					SourceInspectionID *string `json:"sourceInspectionId"`
					VoiceSummary       struct {
						PendingVoiceItems int `json:"pendingVoiceItems"`
						ConflictItems     int `json:"conflictItems"`
						StaleSuggestions  int `json:"staleSuggestions"`
						NotesWithoutItem  int `json:"notesWithoutItem"`
					} `json:"voiceSummary"`
					Environments []struct {
						VoiceSummary struct {
							PendingVoiceItems int `json:"pendingVoiceItems"`
							ConflictItems     int `json:"conflictItems"`
							StaleSuggestions  int `json:"staleSuggestions"`
							NotesWithoutItem  int `json:"notesWithoutItem"`
						} `json:"voiceSummary"`
						Items []struct {
							ID               string
							Decision         string
							Declared         bool
							AttributeSources []source `json:"attributeSources"`
							VoiceConflicts   []struct {
								AttributeName string  `json:"attributeName"`
								VisualValue   string  `json:"visualValue"`
								VoiceValue    *string `json:"voiceValue"`
							} `json:"voiceConflicts"`
						}
					}
				} `json:"inventoryDraft"`
			}
		}
		raw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, draftQuery, fx.ctx, "", "")
		if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Data.Draft.Environments) != 1 {
			t.Fatalf("draft = %s err=%v", raw, err)
		}
		for _, item := range out.Data.Draft.Environments[0].Items {
			if item.ID == d.item.String() {
				if out.Data.Draft.SourceInspectionID == nil || *out.Data.Draft.SourceInspectionID != fx.seed.InspectionID.String() {
					t.Errorf("IT-126 sourceInspectionId = %v", out.Data.Draft.SourceInspectionID)
				}
				return item.AttributeSources
			}
		}
		t.Fatalf("item missing in %s", raw)
		return nil
	}
	sources := read()
	// One pending item cites the same note for two attributes; summary counts items.
	var projection struct {
		Data struct {
			Draft struct {
				VoiceSummary struct{ PendingVoiceItems, StaleSuggestions, NotesWithoutItem int } `json:"voiceSummary"`
			} `json:"inventoryDraft"`
		} `json:"data"`
	}
	projectionRaw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, draftQuery, fx.ctx, "", "")
	if err := json.Unmarshal([]byte(projectionRaw), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.Data.Draft.VoiceSummary.PendingVoiceItems != 1 || projection.Data.Draft.VoiceSummary.StaleSuggestions != 1 || projection.Data.Draft.VoiceSummary.NotesWithoutItem != 1 {
		t.Fatalf("IT-136/IT-178 voice summary = %+v", projection.Data.Draft.VoiceSummary)
	}
	displayKey := "tenant/" + fx.seed.TenantID.String() + "/display/it139-" + fx.seed.MediaID.String() + ".jpg"
	if err := fx.h.Store.Client.Put(context.Background(), fx.h.Store.Bucket, displayKey, bytes.NewReader([]byte("display")), int64(len("display")), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: fx.seed.MediaID, ObjectKey: displayKey, Kind: "DISPLAY", SHA256: strings.Repeat("d", 64), ContentType: "image/jpeg", Width: 1, Height: 1, SizeBytes: int64(len("display")), CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	anchorQuery := fmt.Sprintf(`query { audioNote(id:"%s") { environmentName anchor { kind status mediaId mediaStatus approximate previewUrl playbackUrl frames { id timestampMs url status } framesTruncated visualCoverage } revisions { kind authorName } } }`, d.noteA)
	anchorRaw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, anchorQuery, fx.ctx, "", "")
	var anchorResult struct {
		Data struct {
			Note struct {
				EnvironmentName *string `json:"environmentName"`
				Anchor          struct {
					Kind, Status, MediaStatus, VisualCoverage string
					MediaID                                   *string `json:"mediaId"`
					PreviewURL                                *string `json:"previewUrl"`
				} `json:"anchor"`
				Revisions []struct {
					Kind       string
					AuthorName *string `json:"authorName"`
				} `json:"revisions"`
			} `json:"audioNote"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(anchorRaw), &anchorResult); err != nil {
		t.Fatal(err)
	}
	if anchorResult.Data.Note.Anchor.Kind != "PHOTO" || anchorResult.Data.Note.Anchor.Status != "VALID" || anchorResult.Data.Note.Anchor.MediaID == nil || *anchorResult.Data.Note.Anchor.MediaID != fx.seed.MediaID.String() {
		t.Fatalf("IT-126 anchor projection = %s", anchorRaw)
	}
	if anchorResult.Data.Note.Anchor.MediaStatus != "READY" || anchorResult.Data.Note.Anchor.VisualCoverage != "PHOTO" || anchorResult.Data.Note.Anchor.PreviewURL == nil {
		t.Fatalf("IT-139 authorized photo preview = %s", anchorRaw)
	}
	if anchorResult.Data.Note.EnvironmentName == nil {
		t.Errorf("IT-126 environment name missing: %s", anchorRaw)
	}
	for _, revision := range anchorResult.Data.Note.Revisions {
		if revision.Kind == "HUMAN_EDIT" && (revision.AuthorName == nil || *revision.AuthorName == "") {
			t.Errorf("IT-126 human author name missing: %s", anchorRaw)
		}
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	for _, s := range sources {
		if s.SourceKind != "VOICE" || s.NoteID == nil || *s.NoteID != d.noteA.String() || s.RevisionID == nil || *s.RevisionID != d.revisionA.String() || s.VisualEvidenceID != nil || s.ReviewedBy != nil {
			t.Fatalf("source = %+v, want the voice citation of N1/R1 only", s)
		}
		if !s.Stale || s.CurrentRevisionID == nil || *s.CurrentRevisionID != currentRevision.String() {
			t.Errorf("IT-175 stale source projection = %+v", s)
		}
	}
	// IT-196: the decision records the review and keeps the spoken source.
	decide := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"project-decide"}) { items { id attributeSources { attributeName sourceKind reviewedBy } } userErrors { code message } } }`, d.version, d.item)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, decide, fx.ctx, "", "")
	if strings.Contains(response, `"userErrors":[{`) || !strings.Contains(response, `"sourceKind":"HUMAN_REVIEW"`) || !strings.Contains(response, `"sourceKind":"VOICE"`) {
		t.Fatalf("decision response = %s", response)
	}
	reviewed := read()
	kinds := map[string]int{}
	for _, s := range reviewed {
		kinds[s.SourceKind]++
		if s.SourceKind == "HUMAN_REVIEW" && (s.ReviewedBy == nil || *s.ReviewedBy != fx.seed.MembershipID.String() || s.NoteID != nil) {
			t.Fatalf("review = %+v", s)
		}
	}
	if kinds["VOICE"] != 2 || kinds["HUMAN_REVIEW"] != 2 {
		t.Fatalf("after the decision kinds = %v, want both spoken sources kept and both reviewed", kinds)
	}
}

func TestAudioNoteMediaLinksRequireInspectionEvidenceAccessIT146(t *testing.T) {
	fx, d := seedVoiceDraft(t, auth.Viewer)
	query := fmt.Sprintf(`query { audioNote(id:"%s") { status anchor { kind status mediaStatus previewUrl playbackUrl frames { status url } } revisions { text } } }`, d.noteA)
	var out struct {
		Data struct {
			Note *struct {
				Status string `json:"status"`
				Anchor struct {
					Kind        string  `json:"kind"`
					Status      string  `json:"status"`
					MediaStatus string  `json:"mediaStatus"`
					PreviewURL  *string `json:"previewUrl"`
					PlaybackURL *string `json:"playbackUrl"`
					Frames      []struct {
						Status string  `json:"status"`
						URL    *string `json:"url"`
					} `json:"frames"`
				} `json:"anchor"`
				Revisions []struct{ Text string } `json:"revisions"`
			} `json:"audioNote"`
		} `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	raw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Errors) != 0 || out.Data.Note == nil {
		t.Fatalf("IT-146 viewer can read note but query failed: %s", raw)
	}
	if out.Data.Note.Status != "COMPLETED" || len(out.Data.Note.Revisions) == 0 || out.Data.Note.Anchor.Status != "VALID" {
		t.Fatalf("IT-146 note text/status must remain available: %s", raw)
	}
	if out.Data.Note.Anchor.PreviewURL != nil || out.Data.Note.Anchor.PlaybackURL != nil {
		t.Fatalf("IT-146 unauthorized viewer received media URLs: %s", raw)
	}
	for _, frame := range out.Data.Note.Anchor.Frames {
		if frame.URL != nil {
			t.Fatalf("IT-146 unauthorized viewer received frame URL: %+v", frame)
		}
	}
}

func TestRegenerationIsNewWorkOnTheSourceInspectionB2(t *testing.T) {
	for _, role := range []string{auth.Employee, auth.TenantAdmin} {
		t.Run(role+" cannot regenerate for an archived asset", func(t *testing.T) {
			fx, d := seedVoiceDraft(t, role)
			revisions := fmt.Sprintf(`["%s","%s"]`, d.revisionA, d.revisionB)
			if err := fx.h.AdminDB.Exec("UPDATE assets.assets SET status='ARCHIVED' WHERE tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.AssetID).Error; err != nil {
				t.Fatal(err)
			}
			out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, revisions, "regen-archived-"+role)
			if errs := out.Data.Regenerate.UserErrors; len(errs) != 1 || errs[0].Code != "NOT_FOUND" || out.Data.Regenerate.JobID != nil {
				t.Fatalf("response = %+v, want NOT_FOUND without a run", out)
			}
			var jobs int64
			fx.h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND generation>1", fx.seed.TenantID, d.version).Count(&jobs)
			if jobs != 0 {
				t.Fatalf("an archived asset scheduled %d runs", jobs)
			}
		})
	}
	t.Run("an archived inspection is refused too", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		if err := fx.h.AdminDB.Exec("UPDATE inspections.inspections SET status='ARCHIVED' WHERE tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Error; err != nil {
			t.Skipf("inspection status ARCHIVED is not storable: %v", err)
		}
		out := regenerate(t, fx, d, d.version.String(), d.environment.String(), d.originRevision, fmt.Sprintf(`["%s"]`, d.revisionB), "regen-archived-inspection")
		if errs := out.Data.Regenerate.UserErrors; len(errs) != 1 || errs[0].Code != "NOT_FOUND" {
			t.Fatalf("response = %+v", out)
		}
	})
}

func TestDecideInventoryItemsReturnsEverySourceWithoutPerItemReads(t *testing.T) {
	fx, d := seedVoiceDraft(t, auth.Employee)
	second := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		confidence := 9000
		if err := tx.Create(&database.InventoryItem{ID: second, TenantID: fx.seed.TenantID, OriginVersionID: d.version, EnvironmentID: d.environment, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Copos", Quantity: intPtr(4), Condition: "NOT_ASSESSABLE", Observation: "NOT_ASSESSABLE", ConfidenceBps: &confidence, Origin: "AI", Declared: true, Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		note, revision := d.noteB, d.revisionB
		return tx.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, ItemID: second, OriginVersionID: d.version, AttributeName: "quantity", SourceKind: "VOICE", NoteID: &note, RevisionID: &revision, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1},{itemId:"%s",action:"REJECT",reason:"NOT_EXISTS",expectedVersion:1}],clientMutationId:"decide-two"}) { items { id decision attributeSources { attributeName sourceKind } } userErrors { code message } } }`, d.version, d.item, second)
	var out struct {
		Data struct {
			Decide struct {
				Items []struct {
					ID               string
					Decision         string
					AttributeSources []struct{ AttributeName, SourceKind string } `json:"attributeSources"`
				}
				UserErrors []userErrors
			} `json:"decideInventoryItems"`
		}
	}
	raw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Data.Decide.UserErrors) != 0 || len(out.Data.Decide.Items) != 2 {
		t.Fatalf("response = %s err=%v", raw, err)
	}
	for _, item := range out.Data.Decide.Items {
		kinds := map[string]int{}
		for _, s := range item.AttributeSources {
			kinds[s.SourceKind]++
		}
		if kinds["VOICE"] == 0 || kinds["HUMAN_REVIEW"] == 0 {
			t.Fatalf("item %s (%s) sources = %v, want the spoken source and its review", item.ID, item.Decision, kinds)
		}
	}
}

func TestConfirmAudioNoteTextIT198DoesNotDecideInventoryItem(t *testing.T) {
	fx, d := seedVoiceDraft(t, auth.Employee)
	query := fmt.Sprintf(`mutation { confirmAudioNoteText(input:{noteId:"%s",expectedVersion:1,clientMutationId:"it-198-confirm"}) { note { textConfirmed version } userErrors { code message } } }`, d.noteA)
	raw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if strings.Contains(raw, `"errors"`) || strings.Contains(raw, `"userErrors":[{"code":`) {
		t.Fatalf("IT-198 text confirmation failed: %s", raw)
	}
	var item database.InventoryItem
	var decisions int64
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error; err != nil {
			return err
		}
		return tx.Model(&database.InventoryDecision{}).Where("tenant_id=? AND origin_version_id=? AND target_type='ITEM' AND target_id=?", fx.seed.TenantID, d.version, d.item).Count(&decisions).Error
	}); err != nil {
		t.Fatal(err)
	}
	if item.Decision != "PENDING" || decisions != 0 {
		t.Fatalf("IT-198 text confirmation changed inventory decision=%q decision rows=%d", item.Decision, decisions)
	}
}

func TestDecideVoiceInventoryIT202ActorWithoutDecisionRightIsDenied(t *testing.T) {
	fx, d := seedVoiceDraft(t, auth.Viewer)
	query := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"it-202-denied"}) { items { id decision } userErrors { code message } } }`, d.version, d.item)
	raw := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if !strings.Contains(raw, `"code":"FORBIDDEN"`) || strings.Contains(raw, `"decision":"ACCEPTED"`) {
		t.Fatalf("IT-202 unauthorized decision response = %s", raw)
	}
	var item database.InventoryItem
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, d.item).Take(&item).Error
	}); err != nil {
		t.Fatal(err)
	}
	if item.Decision != "PENDING" {
		t.Fatalf("IT-202 denied request changed item decision to %q", item.Decision)
	}
}
