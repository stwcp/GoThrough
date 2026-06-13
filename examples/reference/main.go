package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/stwcp/GoThrough/proxy"
)

type PathMatcher struct{ Prefix string }

func (m *PathMatcher) Matches(req *http.Request) bool {
	return strings.HasPrefix(req.URL.Path, m.Prefix)
}

type TelemetryLogger struct{}

func (tl *TelemetryLogger) TapRequest(ctx context.Context, req *http.Request) {
	log.Printf("[TAP REQ] Proxy routing: %s %s", req.Method, req.URL.Path)
}

func (tl *TelemetryLogger) TapResponse(ctx context.Context, code int, h http.Header, body io.Reader) {
	log.Printf("[TAP RES] Upstream payload received. Status Code: %d", code)
}

type SecurityGuardrail struct{ RequiredHeader string }

func (sg *SecurityGuardrail) InterceptRequest(req *http.Request) (*http.Response, error) {
	if req.Header.Get(sg.RequiredHeader) == "" {
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error": "Missing mandatory corporate compliance header"}`)),
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	}

	req = proxy.SetMetadata(req, "client-validation-status", "PASSED")
	return nil, nil
}

func main() {
	p := proxy.New("https://api.openai.com")

	logger := &TelemetryLogger{}

	p.OnRequest(&PathMatcher{Prefix: "/v1/chat"}).
		Intercept(&SecurityGuardrail{RequiredHeader: "X-Corporate-Token"}).
		Tap(logger)

	p.OnResponse(&PathMatcher{Prefix: "/v1/chat"}).
		Tap(logger)

	log.Println("Launching customizable proxy engine on port :8080...")
	log.Fatal(http.ListenAndServe(":8080", p.Handler()))
}
