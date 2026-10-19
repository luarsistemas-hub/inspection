package prepare_video_comparisons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const maxMatchImageBytes = 24 << 20

// StoreFrameImages reads, validates and normalizes retained preparation frames.
type StoreFrameImages struct {
	DB     *gorm.DB
	Store  objectstore.Store
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func (s StoreFrameImages) Images(ctx context.Context, frames []Frame) ([]llm.NormalizedImage, error) {
	if s.DB == nil || s.Store.Client == nil {
		return nil, errors.New("analysis/prepare_video_comparisons: missing frame image dependency")
	}
	ids := make([]identity.ID, 0, len(frames))
	var tenantID identity.ID
	for _, frame := range frames {
		if tenantID == (identity.ID{}) {
			tenantID = frame.TenantID
		} else if frame.TenantID != tenantID {
			return nil, fmt.Errorf("%w: frame tenant mismatch", ErrInvalidPlan)
		}
		ids = append(ids, frame.ID)
	}
	var records []database.VideoFrame
	if len(ids) > 0 {
		within := s.Within
		if within == nil {
			within = (tenanttx.Runner{DB: s.DB}).Within
		}
		if err := within(ctx, tenantID, func(tx *gorm.DB) error {
			return tx.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).Find(&records).Error
		}); err != nil {
			return nil, err
		}
	}
	byID := make(map[identity.ID]database.VideoFrame, len(records))
	for _, row := range records {
		byID[row.ID] = row
	}
	images := make([]llm.NormalizedImage, 0, len(frames))
	var total int64
	for _, frame := range frames {
		row, ok := byID[frame.ID]
		if !ok || row.TenantID != frame.TenantID || row.PreparationID != frame.PreparationID || row.SHA256 == "" || row.SHA256 != frame.Digest || row.ObjectKey == "" {
			return nil, fmt.Errorf("%w: frame record does not match selected evidence", ErrInvalidPlan)
		}
		reader, head, err := (objectstore.VideoStore{Store: s.Store}).Open(ctx, row.ObjectKey)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxMatchImageBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(data)) != head.Size || int64(len(data)) != row.SizeBytes && row.SizeBytes > 0 || len(data) > maxMatchImageBytes {
			return nil, objectstore.ErrInvalid
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != row.SHA256 {
			return nil, objectstore.ErrChecksum
		}
		decoded, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, objectstore.ErrInvalid
		}
		encoded, err := encodeMatchJPEG(resizeMatch(decoded, 768), 82)
		if err != nil {
			return nil, err
		}
		total += int64(len(encoded))
		if total > maxMatchImageBytes {
			return nil, ErrImageLimit
		}
		images = append(images, llm.NormalizedImage{EvidenceID: frame.ID.String(), Source: "VIDEO_FRAME", Position: fmt.Sprintf("%d", frame.PTS), DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded), Digest: frame.Digest})
	}
	return images, nil
}

var ErrImageLimit = errors.New("video match image limit exceeded")

func encodeMatchJPEG(img image.Image, quality int) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func resizeMatch(source image.Image, maxEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	long := max(width, height)
	if long <= maxEdge {
		return source
	}
	scale := float64(maxEdge) / float64(long)
	target := image.NewRGBA(image.Rect(0, 0, int(float64(width)*scale), int(float64(height)*scale)))
	for y := 0; y < target.Bounds().Dy(); y++ {
		for x := 0; x < target.Bounds().Dx(); x++ {
			target.Set(x, y, source.At(bounds.Min.X+x*width/target.Bounds().Dx(), bounds.Min.Y+y*height/target.Bounds().Dy()))
		}
	}
	return target
}
