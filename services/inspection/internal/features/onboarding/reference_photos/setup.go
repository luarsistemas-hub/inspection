// Package reference_photos receives private reference images for a verified
// onboarding session before the asset and its origin version exist.
package reference_photos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/onboarding/coordinator"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const path = "/onboarding/reference-photos"

// Dependencies are the external capabilities required by the upload slice.
type Dependencies struct {
	DB       *gorm.DB
	Sessions interface {
		LoadSubmission(context.Context, string, string) (onboardingsession.Submission, error)
	}
	Store  objectstore.Store
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup registers the authenticated, CSRF-protected image upload endpoint.
func Setup(mux *http.ServeMux, d Dependencies) error {
	if mux == nil || d.DB == nil || d.Sessions == nil || d.Store.Client == nil || d.Store.Bucket == "" {
		return fmt.Errorf("slice onboarding/reference_photos: missing dependency")
	}
	mux.HandleFunc(path, d.serve)
	return nil
}

func (d Dependencies) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("inspection_onboarding")
	if err != nil || cookie.Value == "" || r.Header.Get("X-CSRF-Token") == "" {
		writeError(w, apperror.New(apperror.Unauthenticated, "session", "verified onboarding session required"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, objectstore.MaxOriginalBytes+1024*1024)
	if err := r.ParseMultipartForm(1024 * 1024); err != nil {
		writeError(w, apperror.New(apperror.InvalidInput, "file", "invalid photo upload"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, apperror.New(apperror.InvalidInput, "file", "photo is required"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, objectstore.MaxOriginalBytes+1))
	if err != nil || len(data) == 0 || len(data) > objectstore.MaxOriginalBytes {
		writeError(w, apperror.New(apperror.InvalidInput, "file", "photo must be at most 20 MiB"))
		return
	}
	mediaID, err := d.upload(r.Context(), cookie.Value, r.Header.Get("X-CSRF-Token"), r.FormValue("clientMutationId"), header.Header.Get("Content-Type"), r.FormValue("description"), r.FormValue("attentionItems"), data)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"mediaId": mediaID.String(), "status": "VERIFIED"})
}

func (d Dependencies) upload(ctx context.Context, locator, csrf, key, contentType, description, rawAttentionItems string, data []byte) (identity.ID, error) {
	if strings.TrimSpace(key) == "" {
		return identity.ID{}, apperror.New(apperror.InvalidInput, "clientMutationId", "invalid upload identifier")
	}
	if err := coordinator.ValidateOriginMedia(contentType, int64(len(data)), description); err != nil {
		return identity.ID{}, err
	}
	attentionItems, err := normalizeAttentionItems(rawAttentionItems)
	if err != nil {
		return identity.ID{}, err
	}
	submission, err := d.Sessions.LoadSubmission(ctx, locator, csrf)
	if err != nil {
		return identity.ID{}, err
	}
	if submission.Session.TenantID == nil || submission.Session.CurrentStep != onboardingsession.StepProperty || submission.Session.State != coordinator.StatePropertySaved {
		return identity.ID{}, apperror.New(apperror.InvalidState, "step", "reference photos are unavailable")
	}
	tenantID, responsibilityID := *submission.Session.TenantID, submission.Session.ID
	idempotencyKey := "onboarding:photo:" + key
	checksum := sha256.Sum256(data)
	expectedHash := hex.EncodeToString(checksum[:])
	var existing database.MediaObject
	err = d.within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", tenantID, responsibilityID, idempotencyKey).First(&existing).Error
	})
	if err == nil {
		if existing.Description != strings.TrimSpace(description) || existing.SizeBytes != int64(len(data)) || existing.ContentType != contentType || existing.SHA256 != expectedHash || !sameAttentionItems(existing.AttentionItems, attentionItems) {
			return identity.ID{}, apperror.New(apperror.Conflict, "clientMutationId", "photo upload has changed")
		}
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return identity.ID{}, err
	}
	mediaID := identity.NewID()
	objectKey, hash, err := d.Store.PutOriginal(ctx, tenantID, mediaID, contentType, data)
	if err != nil {
		if errors.Is(err, objectstore.ErrInvalid) {
			return identity.ID{}, apperror.New(apperror.InvalidInput, "file", "unsupported or invalid photo")
		}
		return identity.ID{}, err
	}
	now := time.Now().UTC()
	err = d.within(ctx, tenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND status NOT IN ?", tenantID, responsibilityID, []string{"ABORTED", "PURGED"}).Count(&count).Error; err != nil {
			return err
		}
		if count >= capturecore.MaxActivePhotos {
			return apperror.New(apperror.InvalidState, "file", "photo limit reached")
		}
		media := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: objectKey, ContentType: contentType, SHA256: hash, SizeBytes: int64(len(data)), Status: "VERIFIED", IdempotencyKey: idempotencyKey, RequirementKey: "reference", Description: strings.TrimSpace(description), AttentionItems: attentionItems, CaptureSource: "GALLERY", Flags: json.RawMessage(`[]`), CreatedAt: now}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: identity.NewID(), Type: "media.verified.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: mediaID, CorrelationID: "onboarding-photo-" + mediaID.String(), Payload: map[string]any{"mediaId": mediaID, "responsibilityId": responsibilityID, "status": "VERIFIED"}})
	})
	if err != nil {
		_ = d.Store.Delete(ctx, objectKey)
		return identity.ID{}, err
	}
	return mediaID, nil
}

const (
	maxAttentionItems     = 20
	maxAttentionItemRunes = 80
)

func normalizeAttentionItems(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage(`[]`), nil
	}
	if strings.TrimSpace(raw) == "null" {
		return nil, apperror.New(apperror.InvalidInput, "attentionItems", "attention items must be a list of text values")
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, apperror.New(apperror.InvalidInput, "attentionItems", "attention items must be a list of text values")
	}
	items := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if utf8.RuneCountInString(item) > maxAttentionItemRunes {
			return nil, apperror.New(apperror.InvalidInput, "attentionItems", "attention items must be at most 80 characters")
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	if len(items) > maxAttentionItems {
		return nil, apperror.New(apperror.InvalidInput, "attentionItems", "at most 20 attention items are allowed")
	}
	return json.Marshal(items)
}

func sameAttentionItems(saved, requested json.RawMessage) bool {
	var savedItems, requestedItems []string
	if err := json.Unmarshal(saved, &savedItems); err != nil {
		return false
	}
	if err := json.Unmarshal(requested, &requestedItems); err != nil || len(savedItems) != len(requestedItems) {
		return false
	}
	for index := range savedItems {
		if !strings.EqualFold(strings.TrimSpace(savedItems[index]), strings.TrimSpace(requestedItems[index])) {
			return false
		}
	}
	return true
}

func (d Dependencies) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if d.Within != nil {
		return d.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: d.DB}).Within(ctx, tenantID, fn)
}

func writeError(w http.ResponseWriter, err error) {
	code, _, message := apperror.Public(err)
	status := http.StatusServiceUnavailable
	switch code {
	case apperror.Unauthenticated, apperror.SessionExpired, apperror.Forbidden:
		status = http.StatusUnauthorized
	case apperror.InvalidInput:
		status = http.StatusUnprocessableEntity
	case apperror.InvalidState, apperror.Conflict:
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message, "code": string(code)})
}
