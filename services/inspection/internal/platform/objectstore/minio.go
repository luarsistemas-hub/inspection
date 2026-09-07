package objectstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOClient struct {
	Client *minio.Client
	Core   *minio.Core
}

func NewMinIO(endpoint, accessKey, secretKey string, secure bool) (*MinIOClient, error) {
	return NewMinIOWithRegion(endpoint, accessKey, secretKey, secure, "")
}

func NewMinIOWithRegion(endpoint, accessKey, secretKey string, secure bool, region string) (*MinIOClient, error) {
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure, Region: region})
	if err != nil {
		return nil, err
	}
	return &MinIOClient{Client: client, Core: &minio.Core{Client: client}}, nil
}

func (m *MinIOClient) CreateMultipart(ctx context.Context, bucket, key, contentType string) (string, error) {
	return m.Core.NewMultipartUpload(ctx, bucket, key, minio.PutObjectOptions{ContentType: contentType})
}
func (m *MinIOClient) PresignPart(ctx context.Context, bucket, key, uploadID string, part int, ttl time.Duration) (string, error) {
	query := make(url.Values)
	query.Set("uploadId", uploadID)
	query.Set("partNumber", strconv.Itoa(part))
	signed, err := m.Client.Presign(ctx, http.MethodPut, bucket, key, ttl, query)
	if err != nil {
		return "", mapMinIOError(err)
	}
	return signed.String(), nil
}
func (m *MinIOClient) CompleteMultipart(ctx context.Context, bucket, key, uploadID string, parts []Part) error {
	completed := make([]minio.CompletePart, len(parts))
	for i, part := range parts {
		completed[i] = minio.CompletePart{PartNumber: part.Number, ETag: part.ETag}
	}
	_, err := m.Core.CompleteMultipartUpload(ctx, bucket, key, uploadID, completed, minio.PutObjectOptions{})
	return mapMinIOError(err)
}
func (m *MinIOClient) AbortMultipart(ctx context.Context, bucket, key, uploadID string) error {
	return mapMinIOError(m.Core.AbortMultipartUpload(ctx, bucket, key, uploadID))
}
func (m *MinIOClient) Head(ctx context.Context, bucket, key string) (Head, error) {
	info, err := m.Client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Head{}, mapMinIOError(err)
	}
	return Head{Size: info.Size, ContentType: info.ContentType, ChecksumSHA256: info.ChecksumSHA256}, nil
}
func (m *MinIOClient) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	object, err := m.Client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, mapMinIOError(err)
	}
	if _, err := object.Stat(); err != nil {
		object.Close()
		return nil, mapMinIOError(err)
	}
	return object, nil
}

func (m *MinIOClient) Put(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error {
	_, err := m.Client.PutObject(ctx, bucket, key, reader, size, minio.PutObjectOptions{ContentType: contentType})
	return mapMinIOError(err)
}
func (m *MinIOClient) Delete(ctx context.Context, bucket, key string) error {
	return mapMinIOError(m.Client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}))
}
func (m *MinIOClient) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	signed, err := m.Client.Presign(ctx, http.MethodGet, bucket, key, ttl, nil)
	if err != nil {
		return "", mapMinIOError(err)
	}
	return signed.String(), nil
}

func mapMinIOError(err error) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "NoSuchKey", "NoSuchUpload", "NoSuchBucket":
		return ErrNotFound
	case "AccessDenied", "SignatureDoesNotMatch":
		return ErrDenied
	case "RequestExpired", "ExpiredToken":
		return ErrExpired
	case "InvalidDigest", "BadDigest":
		return ErrChecksum
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}
