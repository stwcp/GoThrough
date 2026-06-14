package proxy

import (
	"net/http"
	"net/url"
	"sync"
)

// Pipeline is the root proxy configuration: upstream target and middleware chains.
type Pipeline struct {
	mu             sync.RWMutex
	target         *url.URL
	transport      http.RoundTripper
	requestChains  []*RequestChain
	responseChains []*ResponseChain
	tapStrategy    BackpressureStrategy
	tapBufferDepth int
}

// New initializes the reverse proxy targeting a base upstream endpoint.
func New(targetURL string) *Pipeline {
	u, err := url.Parse(targetURL)
	if err != nil {
		u = &url.URL{}
	}
	return &Pipeline{
		target:    u,
		transport: http.DefaultTransport,
	}
}

// WithTapStrategy configures backpressure behavior for passive response tappers.
func (p *Pipeline) WithTapStrategy(strategy BackpressureStrategy, bufferDepth int) *Pipeline {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tapStrategy = strategy
	p.tapBufferDepth = bufferDepth
	return p
}

// WithTransport sets a custom RoundTripper for upstream requests.
func (p *Pipeline) WithTransport(transport http.RoundTripper) *Pipeline {
	p.mu.Lock()
	defer p.mu.Unlock()
	if transport != nil {
		p.transport = transport
	}
	return p
}

// OnRequest opens an entrypoint for matching and mutating incoming requests.
func (p *Pipeline) OnRequest(matcher Matcher) *RequestChain {
	p.mu.Lock()
	defer p.mu.Unlock()
	rc := &RequestChain{
		pipeline: p,
		matcher:  matcher,
	}
	p.requestChains = append(p.requestChains, rc)
	return rc
}

// OnResponse opens an entrypoint for matching and monitoring upstream responses.
func (p *Pipeline) OnResponse(matcher Matcher) *ResponseChain {
	p.mu.Lock()
	defer p.mu.Unlock()
	rc := &ResponseChain{
		pipeline: p,
		matcher:  matcher,
	}
	p.responseChains = append(p.responseChains, rc)
	return rc
}

// Handler compiles all fluent configurations into an execution loop wrapped in an http.Handler.
func (p *Pipeline) Handler() http.Handler {
	return http.HandlerFunc(p.serveHTTP)
}

// RequestChain stores ordered interceptors and tappers for requests.
type RequestChain struct {
	mu           sync.RWMutex
	pipeline     *Pipeline
	matcher      Matcher
	interceptors []RequestInterceptor
	tappers      []RequestTapper
}

// Intercept registers request interceptors on this chain.
func (rc *RequestChain) Intercept(interceptors ...RequestInterceptor) *RequestChain {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.interceptors = append(rc.interceptors, interceptors...)
	return rc
}

// Tap registers passive request observers on this chain.
func (rc *RequestChain) Tap(tappers ...RequestTapper) *RequestChain {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.tappers = append(rc.tappers, tappers...)
	return rc
}

func (rc *RequestChain) snapshot() (Matcher, []RequestInterceptor, []RequestTapper) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	ic := append([]RequestInterceptor(nil), rc.interceptors...)
	tp := append([]RequestTapper(nil), rc.tappers...)
	return rc.matcher, ic, tp
}

// ResponseChain stores ordered interceptors and tappers for responses.
type ResponseChain struct {
	mu           sync.RWMutex
	pipeline     *Pipeline
	matcher      Matcher
	interceptors []ResponseInterceptor
	tappers      []ResponseTapper
}

// Intercept registers response interceptors on this chain.
func (rc *ResponseChain) Intercept(interceptors ...ResponseInterceptor) *ResponseChain {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.interceptors = append(rc.interceptors, interceptors...)
	return rc
}

// Tap registers passive response observers on this chain.
func (rc *ResponseChain) Tap(tappers ...ResponseTapper) *ResponseChain {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.tappers = append(rc.tappers, tappers...)
	return rc
}

func (rc *ResponseChain) snapshot() (Matcher, []ResponseInterceptor, []ResponseTapper) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	ic := append([]ResponseInterceptor(nil), rc.interceptors...)
	tp := append([]ResponseTapper(nil), rc.tappers...)
	return rc.matcher, ic, tp
}

func (p *Pipeline) snapshotChains() ([]*RequestChain, []*ResponseChain) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	req := append([]*RequestChain(nil), p.requestChains...)
	res := append([]*ResponseChain(nil), p.responseChains...)
	return req, res
}

func (p *Pipeline) snapshotTarget() (*url.URL, http.RoundTripper) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	targetCopy := *p.target
	return &targetCopy, p.transport
}

func (p *Pipeline) snapshotTapSettings() (BackpressureStrategy, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.tapStrategy, p.tapBufferDepth
}
