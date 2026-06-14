package proxy

import (
	"context"
	"io"
	"log"
	"net/http"
)

// BackpressureStrategy defines how passive tappers handle slow consumption rates.
type BackpressureStrategy int

const (
	// StrategyBlock pauses the client response stream if a tapper's internal buffer fills up.
	StrategyBlock BackpressureStrategy = iota
	// StrategyDrop discards telemetry chunks when a tapper cannot keep up, protecting client latency.
	StrategyDrop
	// StrategyUnbounded buffers chunks in memory so tappers never block the client stream.
	StrategyUnbounded
)

const defaultTapChannelDepth = 64

type asyncTapWriter struct {
	strategy BackpressureStrategy
	chunks   chan []byte
	in       chan []byte
}

func newAsyncTapWriter(strategy BackpressureStrategy, depth int) *asyncTapWriter {
	if depth <= 0 {
		depth = defaultTapChannelDepth
	}

	w := &asyncTapWriter{
		strategy: strategy,
		chunks:   make(chan []byte, depth),
	}

	if strategy == StrategyUnbounded {
		w.in = make(chan []byte, depth)
		go w.unboundedPump()
	}

	return w
}

func (w *asyncTapWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	chunk := make([]byte, len(p))
	copy(chunk, p)

	switch w.strategy {
	case StrategyDrop:
		select {
		case w.chunks <- chunk:
		default:
		}
	case StrategyUnbounded:
		w.in <- chunk
	case StrategyBlock:
		fallthrough
	default:
		w.chunks <- chunk
	}

	return len(p), nil
}

func (w *asyncTapWriter) Close() error {
	if w.strategy == StrategyUnbounded {
		close(w.in)
	} else {
		close(w.chunks)
	}
	return nil
}

func (w *asyncTapWriter) unboundedPump() {
	defer close(w.chunks)
	var queue [][]byte

	for {
		var first []byte
		var out chan<- []byte

		if len(queue) > 0 {
			first = queue[0]
			out = w.chunks
		}

		select {
		case item, ok := <-w.in:
			if !ok {
				for _, qItem := range queue {
					w.chunks <- qItem
				}
				return
			}
			queue = append(queue, item)

		case out <- first:
			queue = queue[1:]
		}
	}
}

type chunkReader struct {
	chunks <-chan []byte
	cur    []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.cur = chunk
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func startAsyncResponseTap(ctx context.Context, statusCode int, header http.Header, sink *asyncTapWriter, tapper ResponseTapper) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("response tapper panic: %v", rec)
			}
		}()
		reader := &chunkReader{chunks: sink.chunks}
		tapper.TapResponse(ctx, statusCode, header, reader)
		_, _ = io.Copy(io.Discard, reader)
	}()
}
