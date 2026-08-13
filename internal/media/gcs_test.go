package media

import (
	"context"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

func TestGCSUploadUsesADCBackedV4SigningContract(t *testing.T) {
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	var object string
	var options *storage.SignedURLOptions
	s := &GCSStorage{
		signingServiceAccount: "home-app@example-project.iam.gserviceaccount.com",
		ttl:                   15 * time.Minute,
		now:                   func() time.Time { return now },
		signedURL: func(gotObject string, gotOptions *storage.SignedURLOptions) (string, error) {
			object, options = gotObject, gotOptions
			return "https://storage.googleapis.com/bucket/users/u/image.webp?X-Goog-Signature=test", nil
		},
	}
	upload, err := s.CreateUpload(context.Background(), "users/u/image.webp", "image/webp", 456)
	if err != nil {
		t.Fatal(err)
	}
	if object != "users/u/image.webp" || options.Scheme != storage.SigningSchemeV4 || options.Method != http.MethodPut || options.ContentType != "image/webp" || options.GoogleAccessID != s.signingServiceAccount {
		t.Fatalf("object=%q options=%+v", object, options)
	}
	if !options.Expires.Equal(now.Add(15*time.Minute)) || upload.Headers["Content-Type"] != "image/webp" || !upload.ExpiresAt.Equal(options.Expires) {
		t.Fatalf("upload=%+v options=%+v", upload, options)
	}
}

func TestNewGCSStorageValidatesConfigurationBeforeADC(t *testing.T) {
	for _, cfg := range []GCSConfig{
		{UploadTTL: time.Minute},
		{Bucket: "bucket", UploadTTL: 0},
		{Bucket: "bucket", UploadTTL: 7*24*time.Hour + time.Second},
	} {
		if _, err := NewGCSStorage(context.Background(), cfg); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
