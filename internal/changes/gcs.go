package changes

import (
	"context"
	"errors"
	"io"
	"net/http"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
)

// GCS keeps the marker in a Cloud Storage bucket, with the service's own credentials
// (Application Default Credentials). The bucket the API already keeps media in will do: the
// runtime service account can already write it, and the marker lives under a prefix of its
// own that no media key can take (media keys begin with "users/").
type GCS struct {
	bucket *storage.BucketHandle
}

// NewGCS returns the marker's store in bucket. It makes no request until it is used.
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	if bucket == "" {
		return nil, errors.New("changes: a bucket is required")
	}
	client, e := storage.NewClient(ctx)
	if e != nil {
		return nil, e
	}
	return &GCS{bucket: client.Bucket(bucket)}, nil
}

// Generation is one objects.get for metadata: a Class B operation.
func (g *GCS) Generation(ctx context.Context, name string) (int64, error) {
	attrs, e := g.bucket.Object(name).Attrs(ctx)
	if errors.Is(e, storage.ErrObjectNotExist) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	return attrs.Generation, nil
}

// Read is one object download: a Class B operation.
func (g *GCS) Read(ctx context.Context, name string) ([]byte, int64, error) {
	r, e := g.bucket.Object(name).NewReader(ctx)
	if errors.Is(e, storage.ErrObjectNotExist) {
		return nil, 0, nil
	}
	if e != nil {
		return nil, 0, e
	}
	defer r.Close()
	body, e := io.ReadAll(io.LimitReader(r, maxObjectBytes))
	if e != nil {
		return nil, 0, e
	}
	return body, r.Attrs.Generation, nil
}

// Write is one upload, conditional on the generation the caller read: a Class A operation.
// Precondition failures come back as ErrConflict.
func (g *GCS) Write(ctx context.Context, name string, body []byte, generation int64) error {
	object := g.bucket.Object(name)
	if generation == 0 {
		object = object.If(storage.Conditions{DoesNotExist: true})
	} else {
		object = object.If(storage.Conditions{GenerationMatch: generation})
	}
	w := object.NewWriter(ctx)
	// One request rather than a resumable session: the object is a few kilobytes, and a
	// session is a second billed operation for nothing.
	w.ChunkSize = 0
	w.ContentType = "application/json"
	w.CacheControl = "no-store"
	if _, e := w.Write(body); e != nil {
		_ = w.Close()
		return conflict(e)
	}
	return conflict(w.Close())
}

func conflict(e error) error {
	var api *googleapi.Error
	if errors.As(e, &api) && api.Code == http.StatusPreconditionFailed {
		return ErrConflict
	}
	return e
}
