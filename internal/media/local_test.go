package media

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestLocalStorageAuthorizedUploadAndSniffing(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://localhost:8080/uploads", time.Minute, []byte("local-storage-test-signing-key-more-than-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	upload, err := storage.CreateUpload(context.Background(), "users/test/image.png", "image/png", int64(len(png)))
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse(upload.UploadURL)
	req := httptest.NewRequest(http.MethodPut, target.RequestURI(), bytes.NewReader(png))
	req.Header.Set("Content-Type", "image/png")
	rr := httptest.NewRecorder()
	storage.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	info, err := storage.Stat(context.Background(), upload.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(png)) || info.ContentType != "image/png" {
		t.Fatalf("unexpected object info: %+v", info)
	}

	spoof, err := storage.CreateUpload(context.Background(), "users/test/spoof.png", "image/png", 4)
	if err != nil {
		t.Fatal(err)
	}
	target, _ = url.Parse(spoof.UploadURL)
	req = httptest.NewRequest(http.MethodPut, target.RequestURI(), bytes.NewReader([]byte("nope")))
	req.Header.Set("Content-Type", "image/png")
	rr = httptest.NewRecorder()
	storage.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("spoof status=%d", rr.Code)
	}
}
