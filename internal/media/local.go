package media

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type localDeclaration struct {
	mime string
	size int64
	exp  int64
}

// LocalStorage is an authorized PUT + public GET filesystem provider for local
// development. It exercises the same CreateUpload/Stat/Complete contract as S3.
type LocalStorage struct {
	dir, publicURL string
	ttl            time.Duration
	key            []byte
	mu             sync.Mutex
	pending        map[string]localDeclaration
}

func NewLocalStorage(dir, publicURL string, ttl time.Duration, signingKey []byte) (*LocalStorage, error) {
	if len(signingKey) < 32 {
		return nil, fmt.Errorf("local storage signing key must contain at least 32 bytes")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &LocalStorage{dir: abs, publicURL: strings.TrimRight(publicURL, "/"), ttl: ttl, key: append([]byte(nil), signingKey...), pending: map[string]localDeclaration{}}, nil
}

func (s *LocalStorage) CreateUpload(_ context.Context, key, mime string, size int64) (Upload, error) {
	exp := time.Now().Add(s.ttl).Unix()
	d := localDeclaration{mime: mime, size: size, exp: exp}
	s.mu.Lock()
	s.pending[key] = d
	s.mu.Unlock()
	q := url.Values{"expires": {strconv.FormatInt(exp, 10)}, "signature": {s.sign(key, d)}}
	return Upload{StorageKey: key, UploadURL: s.publicURL + "/" + key + "?" + q.Encode(), Headers: map[string]string{"Content-Type": mime}, ExpiresAt: time.Unix(exp, 0)}, nil
}

func (s *LocalStorage) Stat(_ context.Context, key string) (ObjectInfo, error) {
	path, err := s.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ObjectInfo{}, err
	}
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return ObjectInfo{}, err
	}
	return ObjectInfo{Size: info.Size(), ContentType: http.DetectContentType(buf[:n])}, nil
}

func (s *LocalStorage) ReadURL(_ context.Context, key string) (string, error) {
	if _, err := s.path(key); err != nil {
		return "", err
	}
	return s.publicURL + "/" + key, nil
}

func (s *LocalStorage) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalStorage) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/uploads/"), "/")
		path, err := s.path(key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			http.ServeFile(w, r, path)
		case http.MethodPut:
			s.put(w, r, key, path)
		default:
			w.Header().Set("Allow", "GET, HEAD, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *LocalStorage) put(w http.ResponseWriter, r *http.Request, key, path string) {
	s.mu.Lock()
	d, ok := s.pending[key]
	s.mu.Unlock()
	exp, _ := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	gotSignature := r.URL.Query().Get("signature")
	if !ok || exp != d.exp || time.Now().Unix() > d.exp || !hmac.Equal([]byte(gotSignature), []byte(s.sign(key, d))) {
		http.Error(w, "upload authorization expired or invalid", http.StatusUnauthorized)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]), d.mime) {
		http.Error(w, "content type does not match upload declaration", http.StatusUnprocessableEntity)
		return
	}
	if r.ContentLength != d.size {
		http.Error(w, "content length does not match upload declaration", http.StatusUnprocessableEntity)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	written, copyErr := io.Copy(tmp, io.LimitReader(r.Body, d.size+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written != d.size {
		http.Error(w, "upload body does not match declaration", http.StatusUnprocessableEntity)
		return
	}
	actual, err := sniffFile(tmpName)
	if err != nil || !strings.EqualFold(actual, d.mime) {
		http.Error(w, "file content does not match declared image type", http.StatusUnprocessableEntity)
		return
	}
	if err = os.Rename(tmpName, path); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	delete(s.pending, key)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func sniffFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

func (s *LocalStorage) path(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid object key")
	}
	path := filepath.Join(s.dir, clean)
	if !strings.HasPrefix(path, s.dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid object key")
	}
	return path, nil
}

func (s *LocalStorage) sign(key string, d localDeclaration) string {
	h := hmac.New(sha256.New, s.key)
	fmt.Fprintf(h, "%s\n%s\n%d\n%d", key, d.mime, d.size, d.exp)
	return hex.EncodeToString(h.Sum(nil))
}
