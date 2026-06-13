package proxy

import (
	"context"
	"io"
	"net/http"
)

// Matcher evaluates request attributes to route execution flows conditionally.
type Matcher interface {
	Matches(req *http.Request) bool
}

// RequestInterceptor evaluates or actively mutates an incoming request.
// If a non-nil *http.Response is returned, the request lifecycle short-circuits:
// the remaining chain aborts, and the provided response is immediately served to the client.
type RequestInterceptor interface {
	InterceptRequest(req *http.Request) (*http.Response, error)
}

// ResponseInterceptor evaluates or actively mutates an upstream response
// before data is flushed down to the client.
type ResponseInterceptor interface {
	InterceptResponse(res *http.Response) error
}

// RequestTapper acts as a passive, read-only observer for an incoming request.
// It executes without introducing latency to the request processing path.
type RequestTapper interface {
	TapRequest(ctx context.Context, req *http.Request)
}

// ResponseTapper acts as a passive, read-only observer for an upstream response.
// For streaming responses (such as SSE), it receives a split reader to process
// chunks asynchronously without starving the primary network connection.
type ResponseTapper interface {
	TapResponse(ctx context.Context, statusCode int, header http.Header, body io.Reader)
}
