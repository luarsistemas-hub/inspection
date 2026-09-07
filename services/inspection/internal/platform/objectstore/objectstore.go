package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"time"

	"inspection/libs/identity"

	_ "github.com/gen2brain/h265/heic"
	_ "golang.org/x/image/webp"
)

const (
	MaxOriginalBytes = 20 * 1024 * 1024
	PartSizeBytes    = 5 * 1024 * 1024
)

var (
	ErrNotFound = errors.New("object not found")
	ErrExpired  = errors.New("object operation expired")
	ErrChecksum = errors.New("object checksum mismatch")
	ErrDenied   = errors.New("object access denied")
	ErrInvalid  = errors.New("invalid object")
)

type Part struct {
	Number int
	ETag   string
}
type Head struct {
	Size           int64
	ContentType    string
	ChecksumSHA256 string
}
type PresignedPart struct {
	URL        string
	ExpiresAt  time.Time
	PartNumber int
}

type Client interface {
	CreateMultipart(context.Context, string, string, string) (string, error)
	PresignPart(context.Context, string, string, string, int, time.Duration) (string, error)
	CompleteMultipart(context.Context, string, string, string, []Part) error
	AbortMultipart(context.Context, string, string, string) error
	Head(context.Context, string, string) (Head, error)
	Get(context.Context, string, string) (io.ReadCloser, error)
	Put(context.Context, string, string, io.Reader, int64, string) error
}

type Deleter interface {
	Delete(context.Context, string, string) error
}

type Presigner interface {
	PresignGet(context.Context, string, string, time.Duration) (string, error)
}

type Store struct {
	Bucket       string
	Client       Client
	PublicClient Client
	Clock        func() time.Time
}

type OrphanUpload struct {
	Key, UploadID string
	ExpiresAt     time.Time
	Reconciled    bool
	Active        bool
}
type MultipartCatalog interface {
	CleanupCandidates(context.Context, time.Time) ([]OrphanUpload, error)
	MarkAborted(context.Context, OrphanUpload) error
}

func (s Store) Cleanup(ctx context.Context, catalog MultipartCatalog) (int, error) {
	if catalog == nil || s.Client == nil {
		return 0, ErrInvalid
	}
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	uploads, err := catalog.CleanupCandidates(ctx, now)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, upload := range uploads {
		if upload.Active || !upload.Reconciled || now.Before(upload.ExpiresAt) {
			continue
		}
		if err := s.Client.AbortMultipart(ctx, s.Bucket, upload.Key, upload.UploadID); err != nil && !errors.Is(mapError(err), ErrNotFound) {
			return cleaned, mapError(err)
		}
		if err := catalog.MarkAborted(ctx, upload); err != nil {
			return cleaned, err
		}
		cleaned++
	}
	return cleaned, nil
}

func (s Store) CreateMultipart(ctx context.Context, tenantID identity.ID, mediaID identity.ID, contentType string) (string, string, error) {
	if s.Client == nil || s.Bucket == "" || tenantID == (identity.ID{}) || mediaID == (identity.ID{}) || !SupportedType(contentType) {
		return "", "", ErrInvalid
	}
	key, err := opaqueKey(tenantID, mediaID)
	if err != nil {
		return "", "", err
	}
	uploadID, err := s.Client.CreateMultipart(ctx, s.Bucket, key, contentType)
	if err != nil {
		return "", "", mapError(err)
	}
	return key, uploadID, nil
}

func (s Store) PresignPart(ctx context.Context, key, uploadID string, part int, ttl time.Duration) (PresignedPart, error) {
	if s.Client == nil || key == "" || uploadID == "" || part < 1 || ttl <= 0 || ttl > 15*time.Minute {
		return PresignedPart{}, ErrInvalid
	}
	presigner := s.Client
	if s.PublicClient != nil {
		presigner = s.PublicClient
	}
	url, err := presigner.PresignPart(ctx, s.Bucket, key, uploadID, part, ttl)
	if err != nil {
		return PresignedPart{}, mapError(err)
	}
	now := time.Now
	if s.Clock != nil {
		now = s.Clock
	}
	return PresignedPart{URL: url, PartNumber: part, ExpiresAt: now().Add(ttl)}, nil
}

func (s Store) Complete(ctx context.Context, key, uploadID string, parts []Part, expectedSize int64, expectedType, expectedHash string) error {
	if expectedSize <= 0 || expectedSize > MaxOriginalBytes || !SupportedType(expectedType) || len(parts) != ExpectedParts(expectedSize) || !validParts(parts) {
		return ErrInvalid
	}
	// A committed multipart completion removes its upload ID. On retry the
	// immutable object still has to pass every verification below.
	if err := s.Client.CompleteMultipart(ctx, s.Bucket, key, uploadID, parts); err != nil && !errors.Is(err, ErrNotFound) {
		return mapError(err)
	}
	head, err := s.Client.Head(ctx, s.Bucket, key)
	if err != nil {
		return mapError(err)
	}
	if head.Size != expectedSize || !sameMediaType(head.ContentType, expectedType) {
		return ErrInvalid
	}
	if head.ChecksumSHA256 != "" && !strings.EqualFold(head.ChecksumSHA256, expectedHash) {
		return ErrChecksum
	}
	reader, err := s.Client.Get(ctx, s.Bucket, key)
	if err != nil {
		return mapError(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxOriginalBytes+1))
	if err != nil {
		return mapError(err)
	}
	if int64(len(data)) != expectedSize {
		return ErrChecksum
	}
	detected, err := detectMediaType(data)
	if err != nil || !sameMediaType(detected, expectedType) {
		return ErrInvalid
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return ErrInvalid
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ErrInvalid
	}
	h := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(h[:]), expectedHash) {
		return ErrChecksum
	}
	return nil
}

func ExpectedParts(size int64) int {
	if size <= 0 {
		return 0
	}
	return int((size + PartSizeBytes - 1) / PartSizeBytes)
}

func (s Store) Read(ctx context.Context, key string) ([]byte, error) {
	if s.Client == nil || s.Bucket == "" || key == "" {
		return nil, ErrInvalid
	}
	reader, err := s.Client.Get(ctx, s.Bucket, key)
	if err != nil {
		return nil, mapError(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxOriginalBytes+1))
	if err != nil {
		return nil, mapError(err)
	}
	if len(data) == 0 || len(data) > MaxOriginalBytes {
		return nil, ErrInvalid
	}
	return data, nil
}

func (s Store) Delete(ctx context.Context, key string) error {
	deleter, ok := s.Client.(Deleter)
	if !ok || s.Bucket == "" || key == "" {
		return ErrInvalid
	}
	return mapError(deleter.Delete(ctx, s.Bucket, key))
}

func (s Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presignerClient := s.Client
	if s.PublicClient != nil {
		presignerClient = s.PublicClient
	}
	presigner, ok := presignerClient.(Presigner)
	if !ok || s.Bucket == "" || key == "" || ttl <= 0 || ttl > 15*time.Minute {
		return "", ErrInvalid
	}
	return presigner.PresignGet(ctx, s.Bucket, key, ttl)
}

func (s Store) PutDerivative(ctx context.Context, tenantID, mediaID identity.ID, kind, contentType string, data []byte) (string, string, error) {
	if s.Client == nil || s.Bucket == "" || tenantID == (identity.ID{}) || mediaID == (identity.ID{}) || strings.TrimSpace(kind) == "" || len(data) == 0 {
		return "", "", ErrInvalid
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	key := fmt.Sprintf("tenant/%s/derivative/%s/%s-%s", tenantID.String(), mediaID.String(), strings.ToLower(kind), hash)
	if err := s.Client.Put(ctx, s.Bucket, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		return "", "", mapError(err)
	}
	return key, hash, nil
}

func detectMediaType(data []byte) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg", nil
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png", nil
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", nil
	case len(data) >= 12 && string(data[4:8]) == "ftyp" && (strings.HasPrefix(string(data[8:12]), "hei") || string(data[8:12]) == "mif1"):
		return "image/heic", nil
	default:
		return "", ErrInvalid
	}
}

func SupportedType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0])) {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return true
	default:
		return false
	}
}

func validParts(parts []Part) bool {
	for i, part := range parts {
		if part.Number != i+1 || strings.TrimSpace(part.ETag) == "" {
			return false
		}
	}
	return true
}

func sameMediaType(a, b string) bool {
	return canonicalMediaType(a) == canonicalMediaType(b)
}

func canonicalMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	if value == "image/heif" {
		return "image/heic"
	}
	return value
}

func opaqueKey(tenantID, mediaID identity.ID) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("tenant/%s/original/%s/%x", tenantID.String(), mediaID.String(), random), nil
}

func mapError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("dependency unavailable: %w", err)
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrExpired) || errors.Is(err, ErrChecksum) || errors.Is(err, ErrDenied) || errors.Is(err, ErrInvalid) {
		return err
	}
	return fmt.Errorf("dependency unavailable: %w", err)
}
