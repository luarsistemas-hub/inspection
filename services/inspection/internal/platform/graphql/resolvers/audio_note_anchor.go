package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// enrichAudioNotes adds display names and media context with set based reads.
// These reads are best effort: the authorized transcript remains available if
// an optional media or display lookup fails.
func enrichAudioNotes(ctx context.Context, r *Resolver, tenant identity.ID, notes []*graphql1.AudioNote) {
	if len(notes) == 0 || r.DB == nil {
		return
	}
	authorIDs, inspections, responsibilities := []identity.ID{}, []identity.ID{}, []identity.ID{}
	for _, note := range notes {
		if id, err := identity.ParseID(note.InspectionID); err == nil {
			inspections = append(inspections, id)
		}
		if id, err := identity.ParseID(note.ResponsibilityID); err == nil {
			responsibilities = append(responsibilities, id)
		}
		for _, revision := range note.Revisions {
			if revision.AuthorID != nil {
				if id, err := identity.ParseID(*revision.AuthorID); err == nil {
					authorIDs = append(authorIDs, id)
				}
			}
		}
	}
	_ = (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		if len(authorIDs) > 0 {
			var members []database.Membership
			if tx.WithContext(ctx).Select("id,name").Where("tenant_id=? AND id IN ?", tenant, uniqueIDs(authorIDs)).Find(&members).Error == nil {
				names := map[identity.ID]string{}
				for _, m := range members {
					names[m.ID] = m.Name
				}
				for _, n := range notes {
					for _, rev := range n.Revisions {
						if rev.AuthorID != nil {
							if id, e := identity.ParseID(*rev.AuthorID); e == nil && rev.Kind == "HUMAN_EDIT" {
								if name, ok := names[id]; ok {
									rev.AuthorName = &name
								}
							}
						}
					}
				}
			}
		}
		if len(responsibilities) > 0 {
			var drafts []database.CaptureDraft
			if tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id IN ?", tenant, uniqueIDs(responsibilities)).Find(&drafts).Error == nil {
				byResponsibility := map[identity.ID][]struct {
					Key             string
					EnvironmentName string
				}{}
				for _, draft := range drafts {
					var reqs []struct {
						Key             string
						EnvironmentName string
					}
					if json.Unmarshal(draft.Requirements, &reqs) == nil {
						byResponsibility[draft.ResponsibilityID] = reqs
					}
				}
				for _, n := range notes {
					rid, e := identity.ParseID(n.ResponsibilityID)
					if e != nil {
						continue
					}
					for _, req := range byResponsibility[rid] {
						if req.Key == n.RequirementKey && req.EnvironmentName != "" {
							name := req.EnvironmentName
							n.EnvironmentName = &name
							break
						}
					}
				}
			}
		}
		// Participant declarations use the inspection participant's display name.
		var people []struct {
			InspectionID identity.ID
			Name         string
		}
		if len(inspections) > 0 && tx.WithContext(ctx).Table("inspections.inspections i").Select("i.id AS inspection_id,p.name").Joins("JOIN participants.participants p ON p.tenant_id=i.tenant_id AND p.id=i.participant_id").Where("i.tenant_id=? AND i.id IN ?", tenant, uniqueIDs(inspections)).Scan(&people).Error == nil {
			by := map[identity.ID]string{}
			for _, p := range people {
				by[p.InspectionID] = p.Name
			}
			for _, n := range notes {
				if id, e := identity.ParseID(n.InspectionID); e == nil {
					if name, ok := by[id]; ok {
						for _, rev := range n.Revisions {
							if rev.Kind == "PARTICIPANT_DECLARATION" {
								v := name
								rev.AuthorName = &v
							}
						}
					}
				}
			}
		}
		var promotions []database.InspectionOriginPromotion
		if len(inspections) > 0 && tx.WithContext(ctx).Where("tenant_id=? AND inspection_id IN ? AND status='COMPLETED'", tenant, uniqueIDs(inspections)).Find(&promotions).Error == nil {
			versions := []identity.ID{}
			byInspection := map[identity.ID]identity.ID{}
			for _, p := range promotions {
				if p.OriginVersionID != nil {
					versions = append(versions, *p.OriginVersionID)
					byInspection[p.InspectionID] = *p.OriginVersionID
				}
			}
			var envs []database.InventoryEnvironment
			if len(versions) > 0 && tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id IN ?", tenant, versions).Find(&envs).Error == nil {
				for _, n := range notes {
					insp, e := identity.ParseID(n.InspectionID)
					if e != nil {
						continue
					}
					version := byInspection[insp]
					for _, env := range envs {
						if env.OriginVersionID == version && env.RequirementKey != nil && *env.RequirementKey == n.RequirementKey {
							name := env.Name
							n.EnvironmentName = &name
							break
						}
					}
				}
			}
		}
		return nil
	})
	canViewMedia := audioNotesMediaAccess(ctx, r, tenant, notes)
	loadAudioAnchorMedia(ctx, r, tenant, notes, canViewMedia)
}

// audioNotesMediaAccess is evaluated once for the note request. Capture actors
// can read their own responsibility media; Dashboard actors need the same
// inspection read capability and scope used by the evidence endpoint.
func audioNotesMediaAccess(ctx context.Context, r *Resolver, tenant identity.ID, notes []*graphql1.AudioNote) bool {
	if _, external := requestctx.ExternalCredentialsFromContext(ctx); external {
		return true
	}
	meta, ok := requestctx.FromContext(ctx)
	if !ok || audioInternalRole(ctx, r, meta, false, auth.Manager, auth.Employee, auth.Viewer) != nil {
		return false
	}
	if len(notes) == 0 {
		return false
	}
	inspectionID, err := identity.ParseID(notes[0].InspectionID)
	return err == nil && authorizeAudioInspection(ctx, r.DB, meta, inspectionID) == nil && tenant == meta.TenantID
}

func loadAudioAnchorMedia(ctx context.Context, r *Resolver, tenant identity.ID, notes []*graphql1.AudioNote, canViewMedia bool) {
	mediaIDs := []identity.ID{}
	attemptIDs := []identity.ID{}
	noteByMedia := map[identity.ID][]*graphql1.AudioNote{}
	for _, n := range notes {
		raw := n.PhotoMediaID
		if n.Anchor.Kind == "VIDEO_INTERVAL" {
			raw = n.SourceMediaID
			if raw == nil && n.VideoAttemptID != nil {
				if attemptID, err := identity.ParseID(*n.VideoAttemptID); err == nil {
					attemptIDs = append(attemptIDs, attemptID)
				}
			}
		}
		if raw != nil {
			if id, e := identity.ParseID(*raw); e == nil {
				mediaIDs = append(mediaIDs, id)
				noteByMedia[id] = append(noteByMedia[id], n)
			}
		}
	}
	mediaIDs = uniqueIDs(mediaIDs)
	attemptIDs = uniqueIDs(attemptIDs)
	if len(mediaIDs) == 0 && len(attemptIDs) == 0 {
		return
	}
	_ = (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var media []database.MediaObject
		mediaQuery := tx.WithContext(ctx).Where("tenant_id=?", tenant)
		sourceQuery := false
		if len(mediaIDs) > 0 {
			mediaQuery = mediaQuery.Where("id IN ? OR replaces_media_id IN ?", mediaIDs, mediaIDs)
			sourceQuery = true
		}
		if len(attemptIDs) > 0 {
			if sourceQuery {
				mediaQuery = mediaQuery.Or("tenant_id=? AND media_kind='VIDEO' AND video_attempt_id IN ?", tenant, attemptIDs)
			} else {
				mediaQuery = mediaQuery.Where("media_kind='VIDEO' AND video_attempt_id IN ?", attemptIDs)
			}
		}
		if mediaQuery.Find(&media).Error != nil {
			return nil
		}
		replacedMedia := map[identity.ID]struct{}{}
		for _, replacement := range media {
			if replacement.ReplacesMediaID != nil && replacement.Status != "ABORTED" && replacement.Status != "PURGED" {
				replacedMedia[*replacement.ReplacesMediaID] = struct{}{}
			}
		}
		mediaByAttempt := map[identity.ID]database.MediaObject{}
		for _, item := range media {
			if item.MediaKind == "VIDEO" && item.VideoAttemptID != nil {
				mediaByAttempt[*item.VideoAttemptID] = item
			}
		}
		for _, n := range notes {
			if n.Anchor.Kind != "VIDEO_INTERVAL" || n.SourceMediaID != nil || n.VideoAttemptID == nil {
				continue
			}
			attemptID, err := identity.ParseID(*n.VideoAttemptID)
			if err != nil {
				continue
			}
			item, ok := mediaByAttempt[attemptID]
			if !ok {
				continue
			}
			mediaID := item.ID.String()
			n.SourceMediaID = &mediaID
			n.Anchor.MediaID = &mediaID
			mediaIDs = append(mediaIDs, item.ID)
			noteByMedia[item.ID] = append(noteByMedia[item.ID], n)
		}
		mediaIDs = uniqueIDs(mediaIDs)
		videoIDs := []identity.ID{}
		mediaByID := map[identity.ID]database.MediaObject{}
		for _, m := range media {
			mediaByID[m.ID] = m
		}
		for _, id := range mediaIDs {
			if m, ok := mediaByID[id]; ok && m.MediaKind == "VIDEO" {
				videoIDs = append(videoIDs, id)
			}
		}
		videoIDs = uniqueIDs(videoIDs)
		sources := map[identity.ID]database.VideoSource{}
		if len(videoIDs) > 0 {
			var rows []database.VideoSource
			if tx.WithContext(ctx).Where("tenant_id=? AND media_id IN ?", tenant, videoIDs).Find(&rows).Error == nil {
				for _, row := range rows {
					sources[row.MediaID] = row
				}
			}
		}
		preparationRows := []database.VideoPreparationJob{}
		latestPreparationStatus := map[identity.ID]string{}
		if len(videoIDs) > 0 {
			if tx.WithContext(ctx).Where("tenant_id=? AND media_id IN ?", tenant, videoIDs).Order("generation DESC").Find(&preparationRows).Error == nil {
				for _, row := range preparationRows {
					if _, exists := latestPreparationStatus[row.MediaID]; !exists {
						latestPreparationStatus[row.MediaID] = row.Status
					}
				}
			}
		}
		// Notes created before upload are linked and interval-validated using
		// the same source and preparation rows loaded for this page.
		for _, n := range notes {
			if n.Anchor.Kind != "VIDEO_INTERVAL" || n.SourceMediaID == nil || n.AnchorStatus == "VALID" || n.AnchorStatus == "INVALID" {
				continue
			}
			id, err := identity.ParseID(*n.SourceMediaID)
			if err != nil {
				continue
			}
			item, ok := mediaByID[id]
			if !ok || item.Status != "READY" || latestPreparationStatus[id] != "READY" {
				continue
			}
			source, sourceOK := sources[id]
			if !sourceOK || n.StartMs == nil || n.EndMs == nil || *n.StartMs < 0 || *n.EndMs <= *n.StartMs || source.DurationMicros <= 0 || int64(*n.EndMs)*1000 > source.DurationMicros {
				n.AnchorStatus, n.Anchor.Status = "INVALID", "INVALID"
				continue
			}
			n.AnchorStatus, n.Anchor.Status = "VALID", "VALID"
		}
		displays := map[identity.ID]database.MediaDerivative{}
		var displayRows []database.MediaDerivative
		if tx.WithContext(ctx).Where("tenant_id=? AND media_id IN ? AND kind='DISPLAY'", tenant, uniqueIDs(mediaIDs)).Find(&displayRows).Error == nil {
			for _, row := range displayRows {
				displays[row.MediaID] = row
			}
		}
		preparations := map[identity.ID]database.VideoPreparationJob{}
		if len(videoIDs) > 0 {
			for _, row := range preparationRows {
				if row.Status == "READY" {
					if _, exists := preparations[row.MediaID]; exists {
						continue
					}
					preparations[row.MediaID] = row
				}
			}
		}
		// The note does not store a preparation ID, so frames come from the
		// latest READY preparation of the exact source media. Never follow a
		// replacement to another media or generation.
		preparationIDs := make([]identity.ID, 0, len(preparations))
		for _, prep := range preparations {
			preparationIDs = append(preparationIDs, prep.ID)
		}
		framesBySource := map[identity.ID][]database.VideoFrame{}
		if len(preparationIDs) > 0 {
			var rows []database.VideoFrame
			if tx.WithContext(ctx).Where("tenant_id=? AND source_media_id IN ? AND preparation_id IN ?", tenant, videoIDs, preparationIDs).Order("pts_us ASC").Find(&rows).Error == nil {
				for _, row := range rows {
					framesBySource[row.SourceMediaID] = append(framesBySource[row.SourceMediaID], row)
				}
			}
		}
		for _, m := range media {
			for _, n := range noteByMedia[m.ID] {
				a := n.Anchor
				mid := m.ID.String()
				a.MediaID = &mid
				switch m.Status {
				case "PURGED":
					a.MediaStatus = "PURGED"
				case "REPLACED":
					a.MediaStatus = "REPLACED"
				case "ABORTED":
					if _, replaced := replacedMedia[m.ID]; replaced {
						a.MediaStatus = "REPLACED"
					} else {
						a.MediaStatus = "UNAVAILABLE"
					}
				case "READY":
					a.MediaStatus = "READY"
				case "PREPARING", "RECEIVED", "PROCESSING":
					a.MediaStatus = "PREPARING"
				default:
					a.MediaStatus = "UNAVAILABLE"
				}
				if n.AnchorStatus != "VALID" && a.MediaStatus != "REPLACED" && a.MediaStatus != "PURGED" {
					a.MediaStatus = "PENDING"
				}
				if a.Kind == "VIDEO_INTERVAL" && a.MediaStatus == "READY" && latestPreparationStatus[m.ID] != "" && latestPreparationStatus[m.ID] != "READY" {
					a.MediaStatus = "PREPARING"
				}
				if (a.MediaStatus == "READY" || a.MediaStatus == "PREPARING") && n.AnchorStatus == "VALID" {
					if a.Kind == "PHOTO" {
						d, exists := displays[m.ID]
						if !exists || r.Store.Client == nil {
							a.MediaStatus = "UNAVAILABLE"
						} else if _, e := r.Store.Client.Head(ctx, r.Store.Bucket, d.ObjectKey); e != nil {
							a.MediaStatus = "UNAVAILABLE"
						} else {
							a.VisualCoverage = "PHOTO"
							if canViewMedia {
								if signed, e := r.Store.PresignGet(ctx, d.ObjectKey, 5*time.Minute); e == nil {
									a.PreviewURL = &signed
								}
							}
						}
					} else {
						source, sourceOK := sources[m.ID]
						sourceAvailable := sourceOK && source.Status == "RETAINED" && (source.SourceExpiryAt == nil || source.SourceExpiryAt.After(time.Now().UTC()))
						if !sourceAvailable {
							a.MediaStatus = "UNAVAILABLE"
						} else {
							if canViewMedia {
								u := fmt.Sprintf("/media/videos/%s/content?inspectionId=%s", url.PathEscape(mid), url.QueryEscape(n.InspectionID))
								a.PlaybackURL = &u
							}
						}
						lo, hi := 0, int(^uint(0)>>1)
						if n.StartMs != nil {
							lo = *n.StartMs - 2000
						}
						if n.EndMs != nil {
							hi = *n.EndMs + 2000
						}
						var candidates []database.VideoFrame
						if prep, exists := preparations[m.ID]; exists {
							for _, f := range framesBySource[m.ID] {
								if f.PreparationID == prep.ID && f.PTSMicros >= int64(lo)*1000 && f.PTSMicros <= int64(hi)*1000 {
									candidates = append(candidates, f)
								}
							}
						}
						if len(candidates) > 6 {
							a.FramesTruncated = true
							candidates = candidates[:6]
						}
						for _, f := range candidates {
							ms := int(f.PTSMicros / 1000)
							frameStatus := "AVAILABLE"
							var frameURL *string
							if r.Store.Client == nil {
								frameStatus = "EXPIRED"
							} else if _, e := r.Store.Client.Head(ctx, r.Store.Bucket, f.ObjectKey); e != nil {
								frameStatus = "EXPIRED"
							} else if canViewMedia {
								fu := fmt.Sprintf("/media/video-evidence/%s?inspectionId=%s", url.PathEscape(f.ID.String()), url.QueryEscape(n.InspectionID))
								frameURL = &fu
							}
							a.Frames = append(a.Frames, &graphql1.AudioNoteAnchorFrame{ID: f.ID.String(), TimestampMs: ms, URL: frameURL, Status: frameStatus})
						}
						if len(candidates) > 0 {
							a.VisualCoverage = "FRAMES"
						} else {
							a.VisualCoverage = "LIMITED"
						}
					}
				} else if a.MediaStatus == "UNAVAILABLE" {
					a.VisualCoverage = "NONE"
				}
			}
		}
		return nil
	})
}
