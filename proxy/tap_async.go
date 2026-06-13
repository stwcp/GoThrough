package proxy

import (
	"context"
	"io"
	"log"
	"net/http"
)

const defaultTapChannelDepth = 64

type asyncTapWriter struct {
	chunks chan []byte
}

func newAsyncTapWriter(depth int) *asyncTapWriter {
	if depth <= 0 {
		depth = defaultTapChannelDepth
	}
	return &asyncTapWriter{chunks: make(chan []byte, depth)}
}

func (w *asyncTapWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	chunk := make([]byte, len(p))
	copy(chunk, p)
	w.chunks <- chunk
	return len(p), nil
}

func (w *asyncTapWriter) Close() error {
	close(w.chunks)
	return nil
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
