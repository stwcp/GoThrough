package proxy

import (
	"context"
	"net/http"
	"sync"
)

type contextKey string

const proxyMetadataKey contextKey = "proxy-lifecycle-metadata"

type metadataStore struct {
	mu   sync.RWMutex
	data map[string]any
}

func newMetadataStore() *metadataStore {
	return &metadataStore{data: make(map[string]any)}
}

func (s *metadataStore) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *metadataStore) get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	return val, ok
}

func (s *metadataStore) snapshot() *metadataStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := make(map[string]any, len(s.data))
	for k, v := range s.data {
		copy[k] = v
	}
	return &metadataStore{data: copy}
}

func metadataFromContext(ctx context.Context) (*metadataStore, bool) {
	store, ok := ctx.Value(proxyMetadataKey).(*metadataStore)
	return store, ok
}

// SetMetadata maps a key-value attribute inside the request context, returning the updated request.
func SetMetadata(req *http.Request, key string, value any) *http.Request {
	ctx := req.Context()
	store, ok := metadataFromContext(ctx)
	if !ok {
		store = newMetadataStore()
		ctx = context.WithValue(ctx, proxyMetadataKey, store)
		req = req.WithContext(ctx)
	}
	store.set(key, value)
	return req
}

// GetMetadata safely attempts to extract a shared variable from a processing lifecycle context.
func GetMetadata(ctx context.Context, key string) (any, bool) {
	store, ok := metadataFromContext(ctx)
	if !ok {
		return nil, false
	}
	return store.get(key)
}

func ensureMetadata(req *http.Request) *http.Request {
	if _, ok := metadataFromContext(req.Context()); ok {
		return req
	}
	ctx := context.WithValue(req.Context(), proxyMetadataKey, newMetadataStore())
	return req.WithContext(ctx)
}

func snapshotMetadataContext(ctx context.Context) context.Context {
	store, ok := metadataFromContext(ctx)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, proxyMetadataKey, store.snapshot())
}
