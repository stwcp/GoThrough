package proxy

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsyncTapWriterStrategyDrop(t *testing.T) {
	sink := newAsyncTapWriter(StrategyDrop, 2)

	for i := 0; i < 10; i++ {
		if _, err := sink.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	var received atomic.Int64
	go func() {
		defer close(done)
		reader := &chunkReader{chunks: sink.chunks}
		buf := make([]byte, 16)
		for {
			_, err := reader.Read(buf)
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			received.Add(1)
			time.Sleep(50 * time.Millisecond)
		}
	}()

	_ = sink.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tap reader did not finish")
	}

	if received.Load() > 2 {
		t.Fatalf("received %d chunks, want at most 2 with depth 2 and slow reader", received.Load())
	}
}

func TestAsyncTapWriterStrategyUnboundedDoesNotBlockWrite(t *testing.T) {
	sink := newAsyncTapWriter(StrategyUnbounded, 1)

	start := time.Now()
	for i := 0; i < 200; i++ {
		if _, err := sink.Write([]byte("token")); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("writes blocked for %v", elapsed)
	}

	go func() {
		reader := &chunkReader{chunks: sink.chunks}
		_, _ = io.Copy(io.Discard, reader)
	}()

	_ = sink.Close()
}
