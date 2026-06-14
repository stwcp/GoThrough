package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/stwcp/GoThrough/proxy"
)

const (
	upstreamAddr = ":9091"
	proxyAddr    = ":8081"
)

// auditTapper records the full upstream payload for compliance review.
// StrategyUnbounded ensures no SSE chunk is dropped while this tapper is slow.
type auditTapper struct {
	mu   sync.Mutex
	logs bytes.Buffer
}

func (a *auditTapper) TapRequest(context.Context, *http.Request) {}

func (a *auditTapper) TapResponse(_ context.Context, code int, _ http.Header, body io.Reader) {
	var capture bytes.Buffer
	_, _ = io.Copy(&capture, body)

	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(&a.logs, "--- audit record status=%d bytes=%d ---\n%s\n",
		code, capture.Len(), capture.String())
	log.Printf("[audit] captured %d bytes (total records buffered in memory)", capture.Len())
}

func main() {
	go startMockSSEUpstream(upstreamAddr)

	audit := &auditTapper{}

	p := proxy.New("http://127.0.0.1"+upstreamAddr).
		WithTapStrategy(proxy.StrategyUnbounded, 16)

	p.OnResponse(matchAll{}).Tap(audit)

	log.Printf("compliance-unbounded example")
	log.Printf("  upstream (mock SSE): http://127.0.0.1%s", upstreamAddr)
	log.Printf("  proxy:               http://127.0.0.1%s", proxyAddr)
	log.Printf("  strategy:            StrategyUnbounded buffer=16")
	log.Printf("  try: curl -N http://127.0.0.1%s/stream", proxyAddr)
	log.Printf("  note: audit data grows in memory until the tapper finishes each stream")
	log.Fatal(http.ListenAndServe(proxyAddr, p.Handler()))
}

type matchAll struct{}

func (matchAll) Matches(*http.Request) bool { return true }

func startMockSSEUpstream(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 200; i++ {
			_, _ = fmt.Fprintf(w, "data: token-%d\n\n", i)
			flusher.Flush()
			time.Sleep(2 * time.Millisecond)
		}
	})
	log.Printf("mock upstream listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
