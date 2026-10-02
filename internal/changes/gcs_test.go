package changes_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/burakaltintas/home-app-api/internal/changes"
	"github.com/google/uuid"
)

// fakeGCS answers the three requests the Cloud Storage client makes for the marker -- a
// metadata read, a download and a conditional single-request upload -- the way the service
// does, so the adapter is checked through the real client without touching a real bucket.
type fakeGCS struct {
	mu         sync.Mutex
	bodies     map[string][]byte
	generation map[string]int64
	next       int64
	requests   []string
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	notFound := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":404,"message":"No such object"}}`)
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/storage/v1/b/"):
		// /storage/v1/b/{bucket}/o/{object}
		name, _ := url.PathUnescape(strings.SplitN(r.URL.EscapedPath(), "/o/", 2)[1])
		g, ok := f.generation[name]
		if !ok {
			notFound()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "generation": strconv.FormatInt(g, 10), "metageneration": "1", "size": strconv.Itoa(len(f.bodies[name]))})
	case r.Method == http.MethodGet:
		// /{bucket}/{object}
		name := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[1]
		g, ok := f.generation[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("X-Goog-Generation", strconv.FormatInt(g, 10))
		w.Header().Set("Content-Length", strconv.Itoa(len(f.bodies[name])))
		_, _ = w.Write(f.bodies[name])
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/"):
		if r.URL.Query().Get("uploadType") != "multipart" {
			http.Error(w, "only single-request uploads are expected", http.StatusBadRequest)
			return
		}
		_, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil {
			http.Error(w, e.Error(), http.StatusBadRequest)
			return
		}
		parts := multipart.NewReader(r.Body, params["boundary"])
		var meta struct {
			Name string `json:"name"`
		}
		part, e := parts.NextPart()
		if e != nil || json.NewDecoder(part).Decode(&meta) != nil {
			http.Error(w, "bad metadata part", http.StatusBadRequest)
			return
		}
		part, e = parts.NextPart()
		if e != nil {
			http.Error(w, "no media part", http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(part)
		if want := r.URL.Query().Get("ifGenerationMatch"); want == "" || want != strconv.FormatInt(f.generation[meta.Name], 10) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `{"error":{"code":412,"message":"At least one of the pre-conditions you specified did not hold."}}`)
			return
		}
		f.next++
		f.bodies[meta.Name], f.generation[meta.Name] = body, f.next
		// The client checks what it sent against the checksum the service reports.
		sum := make([]byte, 4)
		binary.BigEndian.PutUint32(sum, crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": meta.Name, "generation": strconv.FormatInt(f.next, 10), "metageneration": "1",
			"size": strconv.Itoa(len(body)), "crc32c": base64.StdEncoding.EncodeToString(sum)})
	default:
		http.Error(w, fmt.Sprintf("unexpected %s %s", r.Method, r.URL), http.StatusNotImplemented)
	}
}

// The adapter does what the marker relies on, through the real Cloud Storage client: a
// missing object is generation zero, a write conditional on a generation lands once and is
// refused as ErrConflict when the object has moved, and a read returns the generation of what
// it read. Each is one request, so each is one billed operation.
func TestGCSKeepsTheMarkerWithConditionalWrites(t *testing.T) {
	fake := &fakeGCS{bodies: map[string][]byte{}, generation: map[string]int64{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(server.URL, "http://"))
	ctx := context.Background()
	objects, e := changes.NewGCS(ctx, "bucket")
	if e != nil {
		t.Fatal(e)
	}
	if g, e := objects.Generation(ctx, name); e != nil || g != 0 {
		t.Fatalf("a missing object: generation %d, %v", g, e)
	}
	if body, g, e := objects.Read(ctx, name); e != nil || g != 0 || body != nil {
		t.Fatalf("a missing object read as %q, generation %d, %v", body, g, e)
	}
	if e := objects.Write(ctx, name, []byte(`{"seq":1}`), 0); e != nil {
		t.Fatal(e)
	}
	if e := objects.Write(ctx, name, []byte(`{"seq":1}`), 0); !errors.Is(e, changes.ErrConflict) {
		t.Fatalf("creating an object that exists answered %v", e)
	}
	g, e := objects.Generation(ctx, name)
	if e != nil || g == 0 {
		t.Fatalf("generation %d, %v", g, e)
	}
	body, read, e := objects.Read(ctx, name)
	if e != nil || read != g || string(body) != `{"seq":1}` {
		t.Fatalf("read %q at generation %d (metadata says %d), %v", body, read, g, e)
	}
	if e := objects.Write(ctx, name, []byte(`{"seq":2}`), g); e != nil {
		t.Fatal(e)
	}
	if e := objects.Write(ctx, name, []byte(`{"seq":2}`), g); !errors.Is(e, changes.ErrConflict) {
		t.Fatalf("a write against a generation that has moved answered %v", e)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 8 {
		t.Fatalf("%d requests for 8 operations: %v", len(fake.requests), fake.requests)
	}
}

// And the marker over it, end to end: a write told through one instance is learned by the
// other at its next look.
func TestTheMarkerWorksOverGCS(t *testing.T) {
	fake := &fakeGCS{bodies: map[string][]byte{}, generation: map[string]int64{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("STORAGE_EMULATOR_HOST", strings.TrimPrefix(server.URL, "http://"))
	objects, e := changes.NewGCS(context.Background(), "bucket")
	if e != nil {
		t.Fatal(e)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := changes.New(objects, name, time.Nanosecond, quiet)
	b := changes.New(objects, name, time.Nanosecond, quiet)
	shop := uuid.New()
	a.Publish(changes.Change{Stores: []uuid.UUID{shop}, Full: true})
	a.Publish(changes.Change{Reviews: true})
	var told []changes.Change
	b.Check(context.Background(), func(c changes.Change) { told = append(told, c) })
	if len(told) != 1 || !same(told[0].Stores, []uuid.UUID{shop}) || !told[0].Full || !told[0].Reviews {
		t.Fatalf("the other instance was told %+v", told)
	}
}
