package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 stores objects in an S3-compatible bucket via the minio-go
// client. Works against AWS S3, MinIO, R2, GCS (S3-compatible API),
// Backblaze B2, and any other implementation that speaks the S3
// API. The Presign path uses AWS Signature V4; URLs expire after
// the requested TTL with no further state on the server side.
type S3 struct {
	Client *minio.Client
	Bucket string
}

// S3Config bundles the constructor inputs.
type S3Config struct {
	Endpoint        string // host[:port], no scheme; required.
	AccessKeyID     string
	SecretAccessKey string
	Region          string // e.g. "us-east-1"; required.
	Bucket          string // required.
	UseSSL          bool   // false for plain HTTP (MinIO local).
}

// NewS3 returns an S3 Provider configured against cfg.
//
// The minio-go client is concurrency-safe; one instance is reused
// across requests. Constructor errors typically signal misconfigured
// credentials or an unreachable endpoint.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("objectstore.s3: Endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("objectstore.s3: Bucket is required")
	}
	creds := credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  creds,
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("objectstore.s3: client: %w", err)
	}
	return &S3{Client: client, Bucket: cfg.Bucket}, nil
}

// ID returns "s3".
func (s *S3) ID() string { return "s3" }

// Put uploads body under key with the given content type. Returns
// nil on success; surfaces minio-go errors verbatim so the caller
// can distinguish auth failures, bucket-not-found, etc.
func (s *S3) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, err := s.Client.PutObject(ctx, s.Bucket, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get fetches the body for key. ErrNotFound is returned when the
// object is missing; other minio-go errors surface as-is.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	obj, err := s.Client.GetObject(ctx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	body, err := io.ReadAll(obj)
	if err != nil {
		// minio-go returns a generic error; the underlying response
		// carries a NoSuchKey code we surface as ErrNotFound.
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return body, nil
}

// GetStream returns a reader over the object body. minio's *Object is
// itself an io.ReadCloser backed by a streaming HTTP response, so the
// §7.2 data plane copies it straight to the client without buffering.
// The metadata is fetched up front via Stat so a missing key surfaces
// as ErrNotFound before any bytes flow.
func (s *S3) GetStream(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return nil, ObjectInfo{}, err
	}
	obj, err := s.Client.GetObject(ctx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}
	return obj, ObjectInfo{Size: stat.Size, ContentType: stat.ContentType}, nil
}

// Stat returns the object's size and content type via a HEAD-style
// StatObject call. ErrNotFound is returned for a missing key.
func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := validateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	stat, err := s.Client.StatObject(ctx, s.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: stat.Size, ContentType: stat.ContentType}, nil
}

// Presign returns an AWS Signature V4 presigned GET URL with the
// given TTL. ttl <= 0 falls back to DefaultPresignTTL.
func (s *S3) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	u, err := s.Client.PresignedGetObject(ctx, s.Bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("objectstore.s3: presign: %w", err)
	}
	return u.String(), nil
}

// Delete removes the object. Missing key is a no-op (S3 returns
// success even when the key wasn't present).
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	return s.Client.RemoveObject(ctx, s.Bucket, key, minio.RemoveObjectOptions{})
}
