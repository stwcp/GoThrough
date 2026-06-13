package proxy

import (
	"bytes"
	"io"
	"sync"
)

const defaultBufferSize = 32 * 1024

var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, defaultBufferSize)
		return &buf
	},
}

func getBuffer() *[]byte {
	return bufferPool.Get().(*[]byte)
}

func putBuffer(buf *[]byte) {
	*buf = (*buf)[:0]
	bufferPool.Put(buf)
}

func readAllPooled(r io.Reader) ([]byte, error) {
	buf := getBuffer()
	defer putBuffer(buf)

	tmp := getBuffer()
	defer putBuffer(tmp)

	*buf = (*buf)[:0]
	for {
		if len(*tmp) == 0 {
			*tmp = make([]byte, defaultBufferSize)
		}
		n, err := r.Read(*tmp)
		if n > 0 {
			*buf = append(*buf, (*tmp)[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}

	out := make([]byte, len(*buf))
	copy(out, *buf)
	return out, nil
}

func copyToPooledWriter(r io.Reader, w io.Writer) (int64, error) {
	buf := getBuffer()
	defer putBuffer(buf)

	if cap(*buf) < defaultBufferSize {
		*buf = make([]byte, defaultBufferSize)
	}
	slice := (*buf)[:defaultBufferSize]

	return io.CopyBuffer(w, r, slice)
}

func bodyReader(body []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(body))
}
