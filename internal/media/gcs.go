package media

import (
	"context"
	"errors"
	"net/http"
	"time"

	"cloud.google.com/go/storage"
)

// GCSConfig uses Application Default Credentials. In production, attach a
// service account through the platform's workload identity mechanism instead
// of distributing a service-account key file.
type GCSConfig struct {
	Bucket                string
	SigningServiceAccount string
	UploadTTL             time.Duration
}

type GCSStorage struct {
	bucket                *storage.BucketHandle
	signingServiceAccount string
	ttl                   time.Duration
	now                   func() time.Time
	signedURL             func(string, *storage.SignedURLOptions) (string, error)
}

func NewGCSStorage(ctx context.Context, cfg GCSConfig) (*GCSStorage, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("GCS bucket is required")
	}
	if cfg.UploadTTL <= 0 || cfg.UploadTTL > 7*24*time.Hour {
		return nil, errors.New("GCS signed upload TTL must be between 1ns and 7 days")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	bucket := client.Bucket(cfg.Bucket)
	return &GCSStorage{
		bucket:                bucket,
		signingServiceAccount: cfg.SigningServiceAccount,
		ttl:                   cfg.UploadTTL,
		now:                   time.Now,
		signedURL:             bucket.SignedURL,
	}, nil
}

func (s *GCSStorage) CreateUpload(_ context.Context, key, mime string, _ int64) (Upload, error) {
	expires := s.now().Add(s.ttl)
	opts := &storage.SignedURLOptions{
		Scheme:         storage.SigningSchemeV4,
		Method:         http.MethodPut,
		Expires:        expires,
		ContentType:    mime,
		GoogleAccessID: s.signingServiceAccount,
	}
	u, err := s.signedURL(key, opts)
	if err != nil {
		return Upload{}, err
	}
	return Upload{StorageKey: key, UploadURL: u, Headers: map[string]string{"Content-Type": mime}, ExpiresAt: expires}, nil
}

func (s *GCSStorage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	attrs, err := s.bucket.Object(key).Attrs(ctx)
	if err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: attrs.Size, ContentType: attrs.ContentType}, nil
}

func (s *GCSStorage) Delete(ctx context.Context, key string) error {
	return s.bucket.Object(key).Delete(ctx)
}
