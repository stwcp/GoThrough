package proxy

import (
	"context"
	"net/http"
)

type contextKey string

const proxyMetadataKey contextKey = "proxy-lifecycle-metadata"

// SetMetadata maps a key-value attribute inside the request context, returning the updated request.
func SetMetadata(req *http.Request, key string, value any) *http.Request {
	ctx := req.Context()
	m, ok := ctx.Value(proxyMetadataKey).(map[string]any)
	if !ok {
		m = make(map[string]any)
		ctx = context.WithValue(ctx, proxyMetadataKey, m)
		req = req.WithContext(ctx)
	}
	m[key] = value
	return req
}

// GetMetadata safely attempts to extract a shared variable from a processing lifecycle context.
func GetMetadata(ctx context.Context, key string) (any, bool) {
	m, ok := ctx.Value(proxyMetadataKey).(map[string]any)
	if !ok {
		return nil, false
	}
	val, exists := m[key]
	return val, exists
}

func ensureMetadata(req *http.Request) *http.Request {
	if _, ok := req.Context().Value(proxyMetadataKey).(map[string]any); ok {
		return req
	}
	ctx := context.WithValue(req.Context(), proxyMetadataKey, make(map[string]any))
	return req.WithContext(ctx)
}
