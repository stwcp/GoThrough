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

type pooledPayload struct {
	mu   sync.Mutex
	buf  *[]byte
	refs int
}

func (p *pooledPayload) acquire() {
	p.mu.Lock()
	p.refs++
	p.mu.Unlock()
}

func (p *pooledPayload) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs <= 0 {
		return
	}
	p.refs--
	if p.refs == 0 && p.buf != nil {
		putBuffer(p.buf)
		p.buf = nil
	}
}

func (p *pooledPayload) discard() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf != nil {
		putBuffer(p.buf)
		p.buf = nil
	}
	p.refs = 0
}

func (p *pooledPayload) len() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf == nil {
		return 0
	}
	return int64(len(*p.buf))
}

func (p *pooledPayload) bytes() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf == nil {
		return nil
	}
	return *p.buf
}

func (p *pooledPayload) reader() io.ReadCloser {
	p.acquire()
	return &pooledBody{
		Reader:  bytes.NewReader(p.bytes()),
		payload: p,
	}
}

type pooledBody struct {
	io.Reader
	payload *pooledPayload
	once    sync.Once
}

func (p *pooledBody) Close() error {
	p.once.Do(func() {
		p.payload.release()
	})
	return nil
}

func readBodyPooled(r io.Reader) (*pooledPayload, error) {
	buf := getBuffer()
	tmp := getBuffer()
	defer putBuffer(tmp)

	*buf = (*buf)[:0]
	for {
		if cap(*tmp) < defaultBufferSize {
			*tmp = make([]byte, defaultBufferSize)
		}
		slice := (*tmp)[:defaultBufferSize]
		n, err := r.Read(slice)
		if n > 0 {
			*buf = append(*buf, slice[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			putBuffer(buf)
			return nil, err
		}
	}

	return &pooledPayload{buf: buf}, nil
}

func readAllPooled(r io.Reader) ([]byte, error) {
	payload, err := readBodyPooled(r)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), payload.bytes()...)
	payload.discard()
	return out, nil
}

func bodyReader(body []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(body))
}
