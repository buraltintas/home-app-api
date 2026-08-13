package media

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDevStorageLifecycle(t *testing.T) {
	s := NewDevStorage(time.Minute)
	u, e := s.CreateUpload(context.Background(), "users/u/x.jpg", "image/jpeg", 123)
	if e != nil || u.StorageKey == "" {
		t.Fatal(e)
	}
	x, e := s.Stat(context.Background(), u.StorageKey)
	if e != nil || x.Size != 123 || x.ContentType != "image/jpeg" {
		t.Fatalf("stat=%+v err=%v", x, e)
	}
	_ = s.Delete(context.Background(), u.StorageKey)
	if _, e = s.Stat(context.Background(), u.StorageKey); e == nil {
		t.Fatal("deleted object still exists")
	}
}
func TestS3PresignUsesDeclaredContent(t *testing.T) {
	s, e := NewS3Storage(context.Background(), S3Config{Region: "auto", Endpoint: "https://example.r2.cloudflarestorage.com", AccessKey: "access", SecretKey: "secret", Bucket: "bucket", PathStyle: true, UploadTTL: 10 * time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	u, e := s.CreateUpload(context.Background(), "users/u/x.webp", "image/webp", 456)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(u.UploadURL, "X-Amz-Signature") || u.Headers["Content-Type"] != "image/webp" {
		t.Fatalf("bad upload: %+v", u)
	}
}
