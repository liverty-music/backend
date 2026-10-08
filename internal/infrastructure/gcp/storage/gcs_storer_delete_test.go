package storage_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	gcs "cloud.google.com/go/storage"
	storage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// fakeGCS serves the subset of the GCS JSON API that object deletion uses:
// listing a bucket by prefix and deleting one object. Objects are kept in
// memory per bucket. A bucket named in failBuckets answers every request with
// 403, which the client does not retry.
type fakeGCS struct {
	mu          sync.Mutex
	objects     map[string]map[string]bool
	failBuckets map[string]bool
}

func newFakeGCS(objects map[string][]string) *fakeGCS {
	f := &fakeGCS{objects: map[string]map[string]bool{}, failBuckets: map[string]bool{}}
	for bucket, names := range objects {
		f.objects[bucket] = map[string]bool{}
		for _, name := range names {
			f.objects[bucket][name] = true
		}
	}
	return f
}

func (f *fakeGCS) exists(bucket, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.objects[bucket][name]
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Paths look like /storage/v1/b/{bucket}/o[/{object}].
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/storage/v1/b/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	bucket, objectPart, _ := strings.Cut(rest, "/o")
	if f.failBuckets[bucket] {
		http.Error(w, `{"error":{"code":403,"message":"forbidden"}}`, http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && objectPart == "":
		prefix := r.URL.Query().Get("prefix")
		items := []map[string]string{}
		for name := range f.objects[bucket] {
			if strings.HasPrefix(name, prefix) {
				items = append(items, map[string]string{"kind": "storage#object", "bucket": bucket, "name": name})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "storage#objects", "items": items})
	case r.Method == http.MethodDelete:
		name, err := url.PathUnescape(strings.TrimPrefix(objectPart, "/"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !f.objects[bucket][name] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"No such object"}}`))
			return
		}
		delete(f.objects[bucket], name)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported", http.StatusNotImplemented)
	}
}

func newFakeStorer(t *testing.T, fake *fakeGCS) *storage.GCSStorer {
	t.Helper()
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	client, err := gcs.NewClient(context.Background(),
		option.WithEndpoint(ts.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	logger, err := logging.New()
	require.NoError(t, err)
	return storage.NewGCSStorerWithClient(client, logger)
}

const (
	testInternalBucket = "organizer-media-internal"
	testServedBucket   = "organizer-media"
	testOrganizerID    = "org-1"
	testMediaID        = "media-1"
)

func TestGCSStorer_DeleteOriginal(t *testing.T) {
	t.Parallel()

	original := storage.OriginalObjectKey(testOrganizerID, testMediaID)
	sibling := storage.OriginalObjectKey(testOrganizerID, "media-2")

	tests := []struct {
		name    string
		objects []string
		fail    bool
		wantErr error
	}{
		{
			// @spec components/entity/media/delete-original "Uploaded original"
			name:    "removes the uploaded original",
			objects: []string{original, sibling},
		},
		{
			// @spec components/entity/media/delete-original "Already removed"
			name:    "succeeds when the original does not exist",
			objects: []string{sibling},
		},
		{
			name:    "returns Internal when storage fails",
			objects: []string{original},
			fail:    true,
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeGCS(map[string][]string{testInternalBucket: tt.objects})
			fake.failBuckets[testInternalBucket] = tt.fail
			s := newFakeStorer(t, fake)

			err := s.DeleteOriginal(context.Background(), testInternalBucket, testOrganizerID, testMediaID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.False(t, fake.exists(testInternalBucket, original))
			assert.True(t, fake.exists(testInternalBucket, sibling), "another media's original must remain")
		})
	}
}

func TestGCSStorer_DeleteVariants(t *testing.T) {
	t.Parallel()

	thumb := storage.VariantObjectKey(testOrganizerID, testMediaID, "thumb")
	large := storage.VariantObjectKey(testOrganizerID, testMediaID, "large")
	sibling := storage.VariantObjectKey(testOrganizerID, "media-2", "thumb")

	tests := []struct {
		name    string
		objects []string
		fail    bool
		wantErr error
	}{
		{
			// @spec components/entity/media/delete-variants "Served cover"
			name:    "removes the thumbnail and the large image",
			objects: []string{thumb, large, sibling},
		},
		{
			// @spec components/entity/media/delete-variants "Already removed"
			name:    "succeeds when no served size exists",
			objects: []string{sibling},
		},
		{
			name:    "returns Internal when storage fails",
			objects: []string{thumb},
			fail:    true,
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeGCS(map[string][]string{testServedBucket: tt.objects})
			fake.failBuckets[testServedBucket] = tt.fail
			s := newFakeStorer(t, fake)

			err := s.DeleteVariants(context.Background(), testServedBucket, testOrganizerID, testMediaID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.False(t, fake.exists(testServedBucket, thumb))
			assert.False(t, fake.exists(testServedBucket, large))
			assert.True(t, fake.exists(testServedBucket, sibling), "another media's variants must remain")
		})
	}
}
