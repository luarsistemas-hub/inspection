package resolvers

import (
	"context"
	"encoding/base64"
	"strconv"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/complete_note_upload"
	"inspection/services/inspection/internal/features/transcription/create_note_upload"
	"inspection/services/inspection/internal/features/transcription/discard_note"
	"inspection/services/inspection/internal/features/transcription/get_note"
	"inspection/services/inspection/internal/features/transcription/list_notes"
	"inspection/services/inspection/internal/features/transcription/retry_note"
	"inspection/services/inspection/internal/features/transcription/revise_note"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func audioNoteIsParticipant(ctx context.Context) bool {
	return !audioNoteHasOperator(ctx) && hasExternalCaptureSession(ctx)
}

// audioNoteHasOperator distinguishes an authenticated operator or trusted
// internal orchestration from ordinary HTTP metadata attached to a capture.
func audioNoteHasOperator(ctx context.Context) bool {
	meta, ok := requestctx.FromContext(ctx)
	return ok && (meta.Principal.Internal || meta.Principal.IdentityID != (identity.ID{}))
}

func hasExternalCaptureSession(ctx context.Context) bool {
	_, ok := requestctx.ExternalCredentialsFromContext(ctx)
	return ok
}

func createAudioNoteUpload(ctx context.Context, r *Resolver, input graphql1.CreateAudioNoteUploadInput) (*graphql1.AudioNoteUploadPayload, error) {
	payload := &graphql1.AudioNoteUploadPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	tenantID, responsibilityID, err := r.externalResponsibility(ctx)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	var photoID, attemptID *identity.ID
	if input.PhotoMediaID != nil {
		id, e := identity.ParseID(*input.PhotoMediaID)
		if e != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "photoMediaId", "A foto selecionada é inválida.")))
			return payload, nil
		}
		photoID = &id
	}
	if input.VideoAttemptID != nil {
		id, e := identity.ParseID(*input.VideoAttemptID)
		if e != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "videoAttemptId", "O vídeo selecionado é inválido.")))
			return payload, nil
		}
		attemptID = &id
	}
	var startMS, endMS *int64
	if input.StartMs != nil {
		v := int64(*input.StartMs)
		startMS = &v
	}
	if input.EndMs != nil {
		v := int64(*input.EndMs)
		endMS = &v
	}
	timeBasis := ""
	if input.TimeBasis != nil {
		timeBasis = *input.TimeBasis
	}
	intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: r.DB, Store: r.Store, Within: r.WithinTenant}, create_note_upload.Command{TenantID: tenantID, ResponsibilityID: responsibilityID, ClientNoteID: input.ClientNoteID, RequirementKey: input.RequirementKey, PhotoMediaID: photoID, VideoAttemptID: attemptID, StartMS: startMS, EndMS: endMS, TimeBasis: timeBasis, ConsentVersion: input.ConsentVersion, DisclosureVersion: input.DisclosureVersion, ContentType: input.ContentType, ContentSHA256: input.ContentSha256, SizeBytes: int64(input.SizeBytes), DurationMS: int64(input.DurationMs)})
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	noteID := intent.NoteID.String()
	payload.NoteID = &noteID
	payload.Upload = &graphql1.AudioNoteUploadIntent{URL: intent.URL, Method: "PUT", Headers: map[string]any{"x-upload-proof": intent.UploadProof}, ExpiresAt: intent.ExpiresAt.Format(time.RFC3339Nano)}
	return payload, nil
}

func audioNoteIdentity(ctx context.Context, r *Resolver, noteID string) (identity.ID, identity.ID, error) {
	parsed, err := identity.ParseID(noteID)
	if err != nil {
		return identity.ID{}, identity.ID{}, apperror.New(apperror.InvalidInput, "noteId", "A nota de áudio informada é inválida.")
	}
	if meta, ok := requestctx.FromContext(ctx); ok && audioNoteHasOperator(ctx) {
		if err := audioInternalRole(ctx, r, meta, false, auth.Manager, auth.Employee, auth.Viewer); err != nil {
			return identity.ID{}, identity.ID{}, err
		}
		row, err := get_note.Setup(ctx, get_note.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
			return withTask06Tenant(ctx, r.DB, tenant, fn)
		}}, get_note.Command{TenantID: meta.TenantID, NoteID: parsed, ScopeOnly: true})
		if err != nil {
			if apperror.Is(err, apperror.NotFound) {
				return identity.ID{}, identity.ID{}, err
			}
			return identity.ID{}, identity.ID{}, apperror.Wrap(apperror.Internal, err)
		}
		if err := authorizeAudioInspection(ctx, r.DB, meta, row.InspectionID); err != nil {
			return identity.ID{}, identity.ID{}, err
		}
		return meta.TenantID, parsed, nil
	}
	var tenantID, responsibilityID identity.ID
	tenantID, responsibilityID, err = r.externalCaptureReadResponsibility(ctx)
	if err != nil {
		return identity.ID{}, identity.ID{}, err
	}
	row, err := get_note.Setup(ctx, get_note.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return withTask06Tenant(ctx, r.DB, tenant, fn)
	}}, get_note.Command{TenantID: tenantID, NoteID: parsed, ScopeOnly: true, IncludeResponsibility: true})
	if err != nil {
		if apperror.Is(err, apperror.NotFound) {
			return identity.ID{}, identity.ID{}, err
		}
		return identity.ID{}, identity.ID{}, apperror.Wrap(apperror.Internal, err)
	}
	if row.ResponsibilityID != responsibilityID {
		return identity.ID{}, identity.ID{}, apperror.New(apperror.NotFound, "noteId", "A nota de áudio não foi encontrada.")
	}
	return tenantID, parsed, nil
}

func completeAudioNoteUpload(ctx context.Context, r *Resolver, input graphql1.CompleteAudioNoteUploadInput) (*graphql1.AudioNotePayload, error) {
	payload := &graphql1.AudioNotePayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	if audioNoteHasOperator(ctx) {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.Forbidden, "noteId", "Somente a captura autorizada pode concluir este envio.")))
		return payload, nil
	}
	tenantID, noteID, err := audioNoteIdentity(ctx, r, input.NoteID)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	err = complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: r.DB, Store: r.Store, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return withTask06Tenant(ctx, r.DB, tenant, fn)
	}}, complete_note_upload.Command{TenantID: tenantID, NoteID: noteID, UploadProof: input.UploadProof, IsParticipant: true})
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	note, err := loadAudioNote(ctx, r, tenantID, noteID)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	payload.Note = note
	return payload, nil
}

func reviseAudioNote(ctx context.Context, r *Resolver, input graphql1.ReviseAudioNoteInput) (*graphql1.AudioNotePayload, error) {
	operation := "REVISE"
	if input.RevisionKind != nil && *input.RevisionKind == graphql1.AudioNoteRevisionKindParticipantDeclaration {
		operation = "DECLARE"
	}
	return mutateAudioNote(ctx, r, input.NoteID, input.ClientMutationID, operation, input.Text, false, input.ExpectedVersion)
}

func loadAudioNote(ctx context.Context, r *Resolver, tenantID, noteID identity.ID) (*graphql1.AudioNote, error) {
	row, err := get_note.Setup(ctx, get_note.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return withTask06Tenant(ctx, r.DB, tenant, fn)
	}}, get_note.Command{TenantID: tenantID, NoteID: noteID})
	if err != nil {
		return nil, err
	}
	out := audioNoteToGraphQL(row)
	enrichAudioNotes(ctx, r, tenantID, []*graphql1.AudioNote{out})
	return out, nil
}

func audioNoteToGraphQL(row get_note.Note) *graphql1.AudioNote {
	out := &graphql1.AudioNote{ID: row.ID.String(), InspectionID: row.InspectionID.String(), EnvironmentID: row.RequirementKey, ResponsibilityID: row.ResponsibilityID.String(), RequirementKey: row.RequirementKey, TargetKind: row.TargetKind, DurationMs: row.DurationMS, Status: row.Status, AnchorStatus: row.AnchorStatus, Highlighted: row.Highlighted, HighlightSource: row.HighlightSource, Version: row.Version, TextConfirmed: row.TextConfirmed, NoSpeech: row.NoSpeech, FailureCode: row.FailureCode, AudioDeletionStatus: row.AudioDeletionStatus, AudioAvailability: audioAvailability(row.AudioDeletionStatus), CreatedAt: row.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.Format(time.RFC3339Nano), Revisions: []*graphql1.AudioNoteRevision{}}
	if row.PhotoMediaID != nil {
		v := row.PhotoMediaID.String()
		out.PhotoMediaID = &v
	}
	if row.VideoAttemptID != nil {
		v := row.VideoAttemptID.String()
		out.VideoAttemptID = &v
	}
	if row.SourceMediaID != nil {
		v := row.SourceMediaID.String()
		out.SourceMediaID = &v
	}
	if row.StartMS != nil {
		v := int(*row.StartMS)
		out.StartMs = &v
	}
	if row.EndMS != nil {
		v := int(*row.EndMS)
		out.EndMs = &v
	}
	if row.CurrentRevisionID != nil {
		v := row.CurrentRevisionID.String()
		out.CurrentRevisionID = &v
	}
	for _, revision := range row.Revisions {
		var author, call *string
		if revision.AuthorID != nil {
			v := revision.AuthorID.String()
			author = &v
		}
		if revision.CallID != nil {
			v := revision.CallID.String()
			call = &v
		}
		out.Revisions = append(out.Revisions, &graphql1.AudioNoteRevision{ID: revision.ID.String(), RevisionNumber: revision.RevisionNumber, Text: revision.Text, Kind: revision.Kind, AuthorID: author, CallID: call, CreatedAt: revision.CreatedAt.Format(time.RFC3339Nano)})
	}
	kind := row.TargetKind
	mediaStatus := "PENDING"
	if row.AnchorStatus == "INVALID" {
		mediaStatus = "UNAVAILABLE"
	}
	out.Anchor = &graphql1.AudioNoteAnchor{Kind: kind, Status: row.AnchorStatus, MediaStatus: mediaStatus, Approximate: kind == "VIDEO_INTERVAL", Frames: []*graphql1.AudioNoteAnchorFrame{}, VisualCoverage: "NONE"}
	return out
}

func audioAvailability(status string) string {
	if status == "DELETED" || status == "EXPIRED" {
		return "UNAVAILABLE"
	}
	return "AVAILABLE"
}

func audioInternalRole(ctx context.Context, r *Resolver, meta requestctx.Metadata, mutate bool, allowed ...string) error {
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, mutate, allowed...); err != nil {
		return apperror.New(apperror.Forbidden, "", "Você não tem permissão para acessar esta nota de áudio.")
	}
	return nil
}

func authorizeAudioInspection(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, inspectionID identity.ID) error {
	if err := authorizeReportInspection(ctx, db, meta, inspectionID); err != nil {
		if apperror.Is(err, apperror.NotFound) {
			return apperror.New(apperror.NotFound, "inspectionId", "A nota de áudio não foi encontrada.")
		}
		if apperror.Is(err, apperror.Forbidden) {
			return apperror.New(apperror.Forbidden, "inspectionId", "Você não tem acesso a esta inspeção.")
		}
		return apperror.Wrap(apperror.Internal, err)
	}
	return nil
}

func getAudioNote(ctx context.Context, r *Resolver, id string) (*graphql1.AudioNote, error) {
	tenantID, noteID, err := audioNoteIdentity(ctx, r, id)
	if err != nil {
		return nil, err
	}
	return loadAudioNote(ctx, r, tenantID, noteID)
}

func listAudioNotes(ctx context.Context, r *Resolver, inspectionID, environmentID string, first *int, after *string) (*graphql1.AudioNoteConnection, error) {
	inspection, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	limit, err := pageLimit(first)
	if err != nil {
		return nil, apperror.New(apperror.PaginationInvalid, "first", "O limite da lista é inválido.")
	}
	offset := 0
	if after != nil && *after != "" {
		decoded, e := base64.RawURLEncoding.DecodeString(*after)
		if e != nil {
			return nil, apperror.New(apperror.PaginationInvalid, "after", "O cursor da lista é inválido.")
		}
		offset, e = strconv.Atoi(string(decoded))
		if e != nil || offset < 0 {
			return nil, apperror.New(apperror.PaginationInvalid, "after", "O cursor da lista é inválido.")
		}
	}
	var tenantID, responsibilityID identity.ID
	if _, external := requestctx.ExternalCredentialsFromContext(ctx); external {
		tenantID, responsibilityID, err = r.externalCaptureReadResponsibility(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		meta, ok := requestctx.FromContext(ctx)
		if !ok {
			return nil, unauthenticated()
		}
		if err := audioInternalRole(ctx, r, meta, false, auth.Manager, auth.Employee, auth.Viewer); err != nil {
			return nil, err
		}
		if err := authorizeAudioInspection(ctx, r.DB, meta, inspection); err != nil {
			return nil, err
		}
		tenantID = meta.TenantID
	}
	page, err := list_notes.Setup(ctx, list_notes.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return withTask06Tenant(ctx, r.DB, tenant, fn)
	}}, list_notes.Command{TenantID: tenantID, InspectionID: inspection, ResponsibilityID: responsibilityID, RequirementKey: environmentID, Offset: offset, Limit: limit})
	if err != nil {
		return nil, err
	}
	connection := &graphql1.AudioNoteConnection{Nodes: []*graphql1.AudioNote{}, PageInfo: &graphql1.PageInfo{HasNextPage: page.HasNextPage}}
	for _, note := range page.Notes {
		connection.Nodes = append(connection.Nodes, audioNoteToGraphQL(note))
	}
	enrichAudioNotes(ctx, r, tenantID, connection.Nodes)
	if len(page.IDs) > 0 {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(page.IDs))))
		connection.PageInfo.EndCursor = &cursor
	}
	return connection, nil
}

func mutateAudioNote(ctx context.Context, r *Resolver, noteID, mutationID, operation, text string, highlighted bool, expectedVersion int) (*graphql1.AudioNotePayload, error) {
	payload := &graphql1.AudioNotePayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: mutationID}
	tenantID, id, err := audioNoteIdentity(ctx, r, noteID)
	if err != nil {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
		return payload, nil
	}
	if meta, exists := requestctx.FromContext(ctx); exists && audioNoteHasOperator(ctx) {
		if operation == "DECLARE" {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.Forbidden, "noteId", "Somente a pessoa responsável pela captura pode registrar esta declaração.")))
			return payload, nil
		}
		allowed := []string{auth.TenantAdmin, auth.Manager, auth.Employee}
		if err := audioInternalRole(ctx, r, meta, true, allowed...); err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
	} else if operation == "CONFIRM" {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.Forbidden, "noteId", "Somente a equipe de revisão pode confirmar o texto.")))
		return payload, nil
	}
	if mutationID == "" {
		payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "clientMutationId", "A identificação da alteração é obrigatória.")))
		return payload, nil
	}
	if operation == "REVISE" || operation == "DECLARE" || operation == "CONFIRM" || operation == "HIGHLIGHT" {
		kind := "HUMAN_EDIT"
		if operation == "DECLARE" {
			kind = "PARTICIPANT_DECLARATION"
		}
		if operation == "CONFIRM" || operation == "HIGHLIGHT" {
			kind = operation
		}
		var authorID *identity.ID
		highlightSource := "PARTICIPANT"
		if meta, exists := requestctx.FromContext(ctx); exists && audioNoteHasOperator(ctx) {
			if meta.Principal.MembershipID != (identity.ID{}) {
				id := meta.Principal.MembershipID
				authorID = &id
			}
			highlightSource = "OPERATOR"
		}
		_, err := revise_note.Setup(ctx, revise_note.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
			return withTask06Tenant(ctx, r.DB, tenantID, fn)
		}}, revise_note.Command{TenantID: tenantID, NoteID: id, ExpectedVersion: expectedVersion, ClientMutationID: mutationID, Text: text, Kind: kind, Highlighted: highlighted, AuthorID: authorID, HighlightSource: highlightSource, IsParticipant: audioNoteIsParticipant(ctx)})
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		note, err := loadAudioNote(ctx, r, tenantID, id)
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		payload.Note = note
		return payload, nil
	}
	if operation == "RETRY" {
		err := retry_note.Setup(ctx, retry_note.Dependencies{DB: r.DB, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
			return withTask06Tenant(ctx, r.DB, tenant, fn)
		}}, retry_note.Command{TenantID: tenantID, NoteID: id, ExpectedVersion: expectedVersion, ClientMutationID: mutationID, IsParticipant: audioNoteIsParticipant(ctx)})
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		note, err := loadAudioNote(ctx, r, tenantID, id)
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		payload.Note = note
		return payload, nil
	}
	if operation == "DISCARD" {
		err := discard_note.Setup(ctx, discard_note.Dependencies{DB: r.DB, Store: r.Store, Within: func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
			return withTask06Tenant(ctx, r.DB, tenant, fn)
		}}, discard_note.Command{TenantID: tenantID, NoteID: id, ExpectedVersion: expectedVersion, ClientMutationID: mutationID, IsParticipant: audioNoteIsParticipant(ctx)})
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		note, err := loadAudioNote(ctx, r, tenantID, id)
		if err != nil {
			payload.UserErrors = append(payload.UserErrors, userErrorFrom(err))
			return payload, nil
		}
		payload.Note = note
		return payload, nil
	}
	payload.UserErrors = append(payload.UserErrors, userErrorFrom(apperror.New(apperror.InvalidInput, "operation", "A operação solicitada é inválida.")))
	return payload, nil
}
