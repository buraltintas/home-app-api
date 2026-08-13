package media

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Upload struct {
	StorageKey, UploadURL string
	Headers               map[string]string
	ExpiresAt             time.Time
}
type ObjectInfo struct {
	Size        int64
	ContentType string
}
type ObjectStorage interface {
	CreateUpload(context.Context, string, string, int64) (Upload, error)
	Stat(context.Context, string) (ObjectInfo, error)
	Delete(context.Context, string) error
}

type S3Config struct {
	Region, Endpoint, AccessKey, SecretKey, Bucket string
	PathStyle                                      bool
	UploadTTL                                      time.Duration
}
type S3Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	ttl     time.Duration
}

func NewS3Storage(ctx context.Context, c S3Config) (*S3Storage, error) {
	cfg, e := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")))
	if e != nil {
		return nil, e
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = c.PathStyle
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	return &S3Storage{client, s3.NewPresignClient(client), c.Bucket, c.UploadTTL}, nil
}
func (s *S3Storage) CreateUpload(ctx context.Context, key, mime string, size int64) (Upload, error) {
	out, e := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, ContentType: &mime, ContentLength: aws.Int64(size)}, func(o *s3.PresignOptions) { o.Expires = s.ttl })
	if e != nil {
		return Upload{}, e
	}
	headers := map[string]string{"Content-Type": mime}
	return Upload{key, out.URL, headers, time.Now().Add(s.ttl)}, nil
}
func (s *S3Storage) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	x, e := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if e != nil {
		return ObjectInfo{}, e
	}
	return ObjectInfo{aws.ToInt64(x.ContentLength), aws.ToString(x.ContentType)}, nil
}
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	_, e := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return e
}

type DevStorage struct {
	TTL     time.Duration
	mu      sync.Mutex
	objects map[string]ObjectInfo
}

func NewDevStorage(ttl time.Duration) *DevStorage {
	return &DevStorage{TTL: ttl, objects: map[string]ObjectInfo{}}
}

func (d *DevStorage) CreateUpload(_ context.Context, key, mime string, size int64) (Upload, error) {
	d.mu.Lock()
	d.objects[key] = ObjectInfo{size, mime}
	d.mu.Unlock()
	return Upload{key, "http://localhost.invalid/uploads/" + key, map[string]string{"Content-Type": mime}, time.Now().Add(d.TTL)}, nil
}
func (d *DevStorage) Stat(_ context.Context, key string) (ObjectInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, ok := d.objects[key]
	if !ok {
		return x, fmt.Errorf("object not found")
	}
	return x, nil
}
func (d *DevStorage) Delete(_ context.Context, key string) error {
	d.mu.Lock()
	delete(d.objects, key)
	d.mu.Unlock()
	return nil
}
