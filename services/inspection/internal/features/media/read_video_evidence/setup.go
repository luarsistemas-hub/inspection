// Package read_video_evidence serves retained frames and video bytes through purpose-scoped reads.
package read_video_evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	DB            *gorm.DB
	Store         objectstore.Store
	Authorizer    auth.Authorizer
	Authenticate  func(*http.Request) (requestctx.Metadata, bool)
	Now           func() time.Time
	MaxConcurrent int
	StreamTimeout time.Duration
}

type Handler struct {
	deps  Dependencies
	slots chan struct{}
}

type ByteRange struct{ Start, End int64 }

// ParseRange accepts one HTTP byte range and returns inclusive offsets.
func ParseRange(value string, size int64) (ByteRange, error) {
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return ByteRange{}, fmt.Errorf("range not satisfiable")
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return ByteRange{}, fmt.Errorf("range not satisfiable")
	}
	var start, end int64
	var err error
	if parts[0] == "" {
		suffix, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || suffix <= 0 {
			return ByteRange{}, fmt.Errorf("range not satisfiable")
		}
		if suffix > size {
			suffix = size
		}
		start, end = size-suffix, size-1
	} else {
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || start < 0 || start >= size {
			return ByteRange{}, fmt.Errorf("range not satisfiable")
		}
		if parts[1] == "" {
			end = size - 1
		} else {
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil || end < start || end >= size {
				return ByteRange{}, fmt.Errorf("range not satisfiable")
			}
		}
	}
	if end-start+1 > objectstore.MaxVideoRange {
		return ByteRange{}, fmt.Errorf("range exceeds streaming limit")
	}
	return ByteRange{Start: start, End: end}, nil
}

func Setup(mux *http.ServeMux, d Dependencies) (*Handler, error) {
	if mux == nil || d.DB == nil || d.Store.Client == nil || d.Store.Bucket == "" || d.Authenticate == nil {
		return nil, fmt.Errorf("slice media/read_video_evidence: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MaxConcurrent <= 0 {
		d.MaxConcurrent = 4
	}
	if d.StreamTimeout <= 0 {
		d.StreamTimeout = 10 * time.Minute
	}
	h := &Handler{deps: d, slots: make(chan struct{}, d.MaxConcurrent)}
	mux.HandleFunc("/media/video-evidence/", h.frame)
	mux.HandleFunc("/media/videos/", h.source)
	return h, nil
}

type access struct {
	metadata    requestctx.Metadata
	media       database.MediaObject
	source      database.VideoSource
	frame       *database.VideoFrame
	objectKey   string
	contentType string
	inspection  identity.ID
}

func (h *Handler) frame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	frameID, err := identity.ParseID(strings.TrimPrefix(r.URL.Path, "/media/video-evidence/"))
	if err != nil || frameID == (identity.ID{}) {
		http.NotFound(w, r)
		return
	}
	item, status := h.resolve(r, frameID, true)
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	if r.Method == http.MethodHead {
		h.frameHeaders(w, item)
		return
	}
	h.stream(w, r, item, 0, item.frame.SizeBytes-1, false, "image/jpeg", item.frame.SizeBytes)
}

func (h *Handler) source(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	mediaID, err := identity.ParseID(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/media/videos/"), "/content"))
	if err != nil || mediaID == (identity.ID{}) || !strings.HasSuffix(r.URL.Path, "/content") {
		http.NotFound(w, r)
		return
	}
	item, status := h.resolve(r, mediaID, false)
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	if item.source.Status == "EXPIRED" || item.source.Status == "DELETING" {
		w.WriteHeader(http.StatusGone)
		return
	}
	if r.Method == http.MethodHead && (item.source.SourceExpiryAt == nil || !item.source.SourceExpiryAt.After(h.deps.Now())) {
		held, err := h.sourceHeld(r.Context(), item.metadata.TenantID, item.media.ID)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if !held {
			w.WriteHeader(http.StatusGone)
			return
		}
	}
	if r.Method == http.MethodHead {
		head, err := h.deps.Store.Client.Head(r.Context(), h.deps.Store.Bucket, item.objectKey)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		start, end := int64(0), head.Size-1
		partial := false
		if value := r.Header.Get("Range"); value != "" {
			selected, parseErr := ParseRange(value, head.Size)
			if parseErr != nil {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", head.Size))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, end, partial = selected.Start, selected.End, true
		}
		h.sourceHeaders(w, item, head.ContentType, start, end, head.Size, partial)
		return
	}
	h.stream(w, r, item, 0, 0, false, "", 0)
}

func (h *Handler) resolve(r *http.Request, id identity.ID, frame bool) (access, int) {
	metadata, ok := h.deps.Authenticate(r)
	if !ok || metadata.TenantID == (identity.ID{}) || metadata.Principal.IdentityID == (identity.ID{}) {
		return access{}, http.StatusUnauthorized
	}
	snapshotText, responsibilityText := r.URL.Query().Get("snapshotId"), r.URL.Query().Get("responsibilityId")
	if (snapshotText == "") == (responsibilityText == "") {
		return access{}, http.StatusNotFound
	}
	if metadata.Principal.Internal {
		responsibilityID, parseErr := identity.ParseID(responsibilityText)
		if snapshotText != "" || parseErr != nil || responsibilityID != metadata.Principal.IdentityID {
			return access{}, http.StatusNotFound
		}
	}
	var item access
	item.metadata = metadata
	var err error
	err = (tenanttx.Runner{DB: h.deps.DB}).Within(r.Context(), metadata.TenantID, func(tx *gorm.DB) error {
		var mediaID identity.ID
		if frame {
			var row database.VideoFrame
			if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=?", metadata.TenantID, id).Take(&row).Error; err != nil {
				return err
			}
			item.frame = &row
			item.objectKey, item.contentType = row.ObjectKey, "image/jpeg"
			mediaID = row.SourceMediaID
		} else {
			mediaID = id
		}
		if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=? AND media_kind='VIDEO'", metadata.TenantID, mediaID).Take(&item.media).Error; err != nil {
			return err
		}
		if err := tx.WithContext(r.Context()).Where("tenant_id=? AND media_id=?", metadata.TenantID, mediaID).Take(&item.source).Error; err != nil {
			return err
		}
		if item.source.ScreeningStatus != "APPROVED" && item.source.ScreeningStatus != "DISABLED" {
			return errNotReady
		}
		if item.frame != nil {
			// Candidate frames persisted during extraction are not evidence
			// until their preparation is READY.
			var preparation database.VideoPreparationJob
			if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=?", metadata.TenantID, item.frame.PreparationID).Take(&preparation).Error; err != nil {
				return err
			}
			if preparation.Status != "READY" {
				return errNotReady
			}
		}
		if !frame {
			item.objectKey, item.contentType = item.media.ObjectKey, item.media.ContentType
			var proxy database.MediaDerivative
			if tx.WithContext(r.Context()).Where("tenant_id=? AND media_id=? AND kind='VIDEO_PLAYBACK'", metadata.TenantID, mediaID).Take(&proxy).Error == nil {
				item.objectKey, item.contentType = proxy.ObjectKey, proxy.ContentType
			}
		}
		if snapshotText != "" {
			snapshotID, parseErr := identity.ParseID(snapshotText)
			if parseErr != nil {
				return gorm.ErrRecordNotFound
			}
			var snapshot database.ReportSnapshot
			if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=?", metadata.TenantID, snapshotID).Take(&snapshot).Error; err != nil {
				return err
			}
			if !snapshotContainsEvidence(snapshot.CanonicalJSON, item.frame, mediaID) {
				return gorm.ErrRecordNotFound
			}
			item.inspection = snapshot.InspectionID
		} else {
			responsibilityID, parseErr := identity.ParseID(responsibilityText)
			if parseErr != nil {
				return gorm.ErrRecordNotFound
			}
			var responsibility database.Responsibility
			if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=?", metadata.TenantID, responsibilityID).Take(&responsibility).Error; err != nil {
				return err
			}
			item.inspection = responsibility.InspectionID
			if responsibility.ID != item.media.ResponsibilityID {
				var draft database.CaptureDraft
				if item.frame == nil || tx.WithContext(r.Context()).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", metadata.TenantID, responsibility.ID).Take(&draft).Error != nil || !referenceContainsFrame(draft.ReferencePayload, item.frame.ID, item.frame.SourceMediaID) {
					return gorm.ErrRecordNotFound
				}
			}
		}
		var inspection database.Inspection
		if err := tx.WithContext(r.Context()).Where("tenant_id=? AND id=?", metadata.TenantID, item.inspection).Take(&inspection).Error; err != nil {
			return err
		}
		if !metadata.Principal.Internal {
			principal, err := h.deps.Authorizer.Authorize(r.Context(), metadata.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee, auth.CustomerViewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: inspection.BusinessUnitID}, false)
			if err != nil {
				return err
			}
			if hasRole(principal.Roles, auth.CustomerViewer) {
				if snapshotText == "" {
					return gorm.ErrRecordNotFound
				}
				var publication database.ReportPublication
				if err := tx.WithContext(r.Context()).Where("tenant_id=? AND snapshot_id=? AND inspection_id=? AND status='PUBLISHED'", metadata.TenantID, snapshotIDOrZero(snapshotText), inspection.ID).Take(&publication).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if errors.Is(err, errNotReady) {
		return access{}, http.StatusConflict
	}
	if err != nil {
		return access{}, http.StatusNotFound
	}
	return item, http.StatusOK
}

var errNotReady = errors.New("video source is not ready")

func referenceContainsFrame(payload []byte, frameID, sourceMediaID identity.ID) bool {
	var document struct {
		Scope []struct {
			FrameID       identity.ID `json:"frameId"`
			SourceMediaID identity.ID `json:"sourceMediaId"`
		} `json:"scope"`
		VideoRecapture struct {
			Selected []struct {
				OriginalMediaID identity.ID   `json:"originalMediaId"`
				OriginFrameIDs  []identity.ID `json:"originFrameIds"`
			} `json:"selected"`
		} `json:"videoRecapture"`
	}
	if json.Unmarshal(payload, &document) != nil {
		return false
	}
	for _, item := range document.Scope {
		if item.FrameID == frameID && item.SourceMediaID == sourceMediaID {
			return true
		}
	}
	for _, selected := range document.VideoRecapture.Selected {
		if selected.OriginalMediaID != sourceMediaID {
			continue
		}
		for _, allowedID := range selected.OriginFrameIDs {
			if allowedID == frameID {
				return true
			}
		}
	}
	return false
}

func snapshotIDOrZero(value string) identity.ID {
	id, _ := identity.ParseID(value)
	return id
}

func snapshotContainsEvidence(canonical []byte, frame *database.VideoFrame, mediaID identity.ID) bool {
	var snapshot report.Snapshot
	if json.Unmarshal(canonical, &snapshot) != nil {
		return false
	}
	for _, evidence := range snapshot.Evidence {
		if evidence.SourceMediaID != mediaID.String() {
			continue
		}
		if frame == nil || evidence.FrameID == frame.ID.String() && evidence.PreparationID == frame.PreparationID.String() && strings.EqualFold(evidence.FrameDigest, frame.SHA256) && strings.EqualFold(evidence.DisplayDigest, frame.SHA256) {
			return true
		}
	}
	return false
}

func hasRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}

func (h *Handler) frameHeaders(w http.ResponseWriter, item access) {
	w.Header().Set("Content-Type", item.contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(item.frame.SizeBytes, 10))
	w.Header().Set("ETag", `"`+item.frame.SHA256+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) sourceHeaders(w http.ResponseWriter, item access, contentType string, start, end, size int64, partial bool) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.WriteHeader(http.StatusPartialContent)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request, item access, start, end int64, partial bool, contentType string, size int64) {
	ctx, cancel := context.WithTimeout(r.Context(), h.deps.StreamTimeout)
	defer cancel()
	if item.frame == nil {
		select {
		case h.slots <- struct{}{}:
			defer func() { <-h.slots }()
		default:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		leaseID, leaseErr := h.acquireSourceLease(r.Context(), item)
		if leaseErr != nil {
			if errors.Is(leaseErr, objectstore.ErrExpired) {
				w.WriteHeader(http.StatusGone)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		defer func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer releaseCancel()
			h.releaseSourceLease(releaseCtx, item.metadata.TenantID, leaseID)
		}()
		head, headErr := h.deps.Store.Client.Head(ctx, h.deps.Store.Bucket, item.objectKey)
		if headErr != nil || head.Size <= 0 || head.Size > objectstore.MaxVideoBytes {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		start, end, contentType, size = 0, head.Size-1, head.ContentType, head.Size
		partial = false
		if value := r.Header.Get("Range"); value != "" {
			selected, parseErr := ParseRange(value, size)
			if parseErr != nil {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, end, partial = selected.Start, selected.End, true
		}
	}
	var reader io.ReadCloser
	store := objectstore.VideoStore{Store: h.deps.Store}
	var err error
	if item.frame != nil {
		data, readErr := verifiedFrameBytes(ctx, h.deps.Store, *item.frame)
		if readErr != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(item.frame.SizeBytes, 10))
		w.Header().Set("ETag", `"`+item.frame.SHA256+`"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	if partial {
		reader, err = store.OpenRange(ctx, item.objectKey, start, end)
	} else {
		reader, _, err = store.Open(ctx, item.objectKey)
	}
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer reader.Close()
	h.sourceHeaders(w, item, contentType, start, end, size, partial)
	_, _ = io.Copy(w, reader)
}

func verifiedFrameBytes(ctx context.Context, store objectstore.Store, frame database.VideoFrame) ([]byte, error) {
	if frame.SizeBytes <= 0 || frame.SizeBytes > objectstore.MaxVideoRange {
		return nil, objectstore.ErrInvalid
	}
	reader, err := (objectstore.VideoStore{Store: store}).OpenRange(ctx, frame.ObjectKey, 0, frame.SizeBytes-1)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || int64(len(data)) != frame.SizeBytes {
		return nil, objectstore.ErrInvalid
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), frame.SHA256) {
		return nil, objectstore.ErrInvalid
	}
	return data, nil
}

func (h *Handler) acquireSourceLease(ctx context.Context, item access) (identity.ID, error) {
	id := identity.NewID()
	now := h.deps.Now().UTC()
	err := (tenanttx.Runner{DB: h.deps.DB}).Within(ctx, item.metadata.TenantID, func(tx *gorm.DB) error {
		var source database.VideoSource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", item.metadata.TenantID, item.media.ID).Take(&source).Error; err != nil {
			return err
		}
		if source.Status == "DELETING" || source.Status == "EXPIRED" {
			return objectstore.ErrExpired
		}
		if source.SourceExpiryAt == nil || !source.SourceExpiryAt.After(now) {
			held, err := retentioncore.EffectiveSourceHeld(tx, item.metadata.TenantID, item.media.ID)
			if err != nil {
				return err
			}
			if !held {
				return objectstore.ErrExpired
			}
		}
		lease := database.VideoSourceLease{ID: id, TenantID: item.metadata.TenantID, SourceMediaID: item.media.ID, Purpose: "PLAYBACK", LeaseUntil: now.Add(h.deps.StreamTimeout), CreatedAt: now}
		return tx.Create(&lease).Error
	})
	return id, err
}

func (h *Handler) sourceHeld(ctx context.Context, tenantID, mediaID identity.ID) (bool, error) {
	var held bool
	err := (tenanttx.Runner{DB: h.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var err error
		held, err = retentioncore.EffectiveSourceHeld(tx, tenantID, mediaID)
		return err
	})
	return held, err
}

func (h *Handler) releaseSourceLease(ctx context.Context, tenantID, leaseID identity.ID) {
	_ = (tenanttx.Runner{DB: h.deps.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", tenantID, leaseID).Delete(&database.VideoSourceLease{}).Error
	})
}
