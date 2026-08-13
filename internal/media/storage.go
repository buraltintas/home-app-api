package media

import "context"

type Upload struct {
	StorageKey, UploadURL string
	Headers               map[string]string
}
type ObjectStorage interface {
	CreateUpload(context.Context, string, string, int64) (Upload, error)
	Delete(context.Context, string) error
}
