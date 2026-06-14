package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/stwcp/GoThrough/proxy"
)

const (
	upstreamAddr = ":9090"
	proxyAddr    = ":8080"
)

// slowMetricsTapper simulates a remote metrics sink that cannot keep up with token streams.
type slowMetricsTapper struct {
	received atomic.Int64
}

func (t *slowMetricsTapper) TapRequest(context.Context, *http.Request) {}

func (t *slowMetricsTapper) TapResponse(_ context.Context, code int, _ http.Header, body io.Reader) {
	log.Printf("[metrics] stream opened status=%d", code)
	buf := make([]byte, 64)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			t.received.Add(1)
			time.Sleep(25 * time.Millisecond)
		}
		if err != nil {
			break
		}
	}
	log.Printf("[metrics] stream closed chunks_received=%d (dropped chunks are not counted)",
		t.received.Load())
}

func main() {
	go startMockSSEUpstream(upstreamAddr)

	tapper := &slowMetricsTapper{}

	p := proxy.New("http://127.0.0.1"+upstreamAddr).
		WithTapStrategy(proxy.StrategyDrop, 8)

	p.OnResponse(matchAll{}).Tap(tapper)

	log.Printf("telemetry-drop example")
	log.Printf("  upstream (mock SSE): http://127.0.0.1%s", upstreamAddr)
	log.Printf("  proxy:               http://127.0.0.1%s", proxyAddr)
	log.Printf("  strategy:            StrategyDrop buffer=8")
	log.Printf("  try: curl -N http://127.0.0.1%s/stream", proxyAddr)
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
