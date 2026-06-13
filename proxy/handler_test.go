package proxy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stwcp/GoThrough/proxy"
)

type alwaysMatcher struct{}

func (alwaysMatcher) Matches(*http.Request) bool { return true }

type headerInterceptor struct {
	header string
}

func (h headerInterceptor) InterceptRequest(req *http.Request) (*http.Response, error) {
	if req.Header.Get(h.header) == "" {
		resp := &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
			Header:     make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	}
	req = proxy.SetMetadata(req, "validated", true)
	return nil, nil
}

type metadataReader struct{}

func (metadataReader) InterceptRequest(req *http.Request) (*http.Response, error) {
	val, ok := proxy.GetMetadata(req.Context(), "validated")
	if !ok || val != true {
		return nil, nil
	}
	req.Header.Set("X-Validated", "true")
	return nil, nil
}

type countingTapper struct {
	requests  atomic.Int64
	responses atomic.Int64
}

func (c *countingTapper) TapRequest(context.Context, *http.Request) {
	c.requests.Add(1)
}

func (c *countingTapper) TapResponse(context.Context, int, http.Header, io.Reader) {
	c.responses.Add(1)
}

func TestSecurityShortCircuit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	t.Cleanup(upstream.Close)

	p := proxy.New(upstream.URL)
	p.OnRequest(alwaysMatcher{}).Intercept(headerInterceptor{header: "X-Corporate-Token"})

	server := httptest.NewServer(p.Handler())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/v1/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestForwardsToUpstreamWithMetadata(t *testing.T) {
	var gotValidated string
	var gotForwardedFor string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotValidated = r.Header.Get("X-Validated")
		gotForwardedFor = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	p := proxy.New(upstream.URL)
	p.OnRequest(alwaysMatcher{}).
		Intercept(headerInterceptor{header: "X-Corporate-Token"}).
		Intercept(metadataReader{})

	server := httptest.NewServer(p.Handler())
	t.Cleanup(server.Close)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Corporate-Token", "secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotValidated != "true" {
		t.Fatalf("X-Validated = %q, want true", gotValidated)
	}
	if gotForwardedFor == "" {
		t.Fatal("expected X-Forwarded-For to be set")
	}
}

func TestTappersRunWithoutBlocking(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)

	tapper := &countingTapper{}
	p := proxy.New(upstream.URL)
	p.OnRequest(alwaysMatcher{}).Tap(tapper)
	p.OnResponse(alwaysMatcher{}).Tap(tapper)

	server := httptest.NewServer(p.Handler())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/v1/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for tapper.requests.Load() == 0 || tapper.responses.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("tappers not invoked: req=%d res=%d", tapper.requests.Load(), tapper.responses.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSetAndGetMetadata(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = proxy.SetMetadata(req, "tier", "gold")

	val, ok := proxy.GetMetadata(req.Context(), "tier")
	if !ok {
		t.Fatal("metadata missing")
	}
	if val != "gold" {
		t.Fatalf("tier = %v, want gold", val)
	}
}

type slowResponseTapper struct {
	started chan struct{}
}

func (s *slowResponseTapper) TapRequest(context.Context, *http.Request) {}

func (s *slowResponseTapper) TapResponse(_ context.Context, _ int, _ http.Header, body io.Reader) {
	close(s.started)
	time.Sleep(2 * time.Second)
	_, _ = io.Copy(io.Discard, body)
}

func TestSlowResponseTapperDoesNotBlockClient(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			_, _ = w.Write([]byte("data: chunk\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(upstream.Close)

	slow := &slowResponseTapper{started: make(chan struct{})}
	p := proxy.New(upstream.URL)
	p.OnResponse(alwaysMatcher{}).Tap(slow)

	server := httptest.NewServer(p.Handler())
	t.Cleanup(server.Close)

	start := time.Now()
	resp, err := http.Get(server.URL + "/v1/chat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if len(body) == 0 {
		t.Fatal("expected streamed body")
	}
	if elapsed > time.Second {
		t.Fatalf("client blocked for %v, want under 1s", elapsed)
	}

	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tapper never started")
	}
}

type metadataWritingTapper struct {
	done chan struct{}
}

func (m *metadataWritingTapper) TapRequest(ctx context.Context, req *http.Request) {
	for i := 0; i < 100; i++ {
		proxy.GetMetadata(ctx, "validated")
	}
	close(m.done)
}

func (m *metadataWritingTapper) TapResponse(context.Context, int, http.Header, io.Reader) {}

func TestMetadataTapRace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	tapper := &metadataWritingTapper{done: make(chan struct{})}
	p := proxy.New(upstream.URL)
	p.OnRequest(alwaysMatcher{}).
		Intercept(headerInterceptor{header: "X-Corporate-Token"}).
		Tap(tapper)

	server := httptest.NewServer(p.Handler())
	t.Cleanup(server.Close)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat", strings.NewReader(`{"model":"gpt-4"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Corporate-Token", "secret")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	select {
	case <-tapper.done:
	case <-time.After(2 * time.Second):
		t.Fatal("tapper did not finish")
	}
}
