package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (p *Pipeline) serveHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("proxy panic: %v", rec)
			writeMaskedError(w, http.StatusInternalServerError, "internal proxy error")
		}
	}()

	r = ensureMetadata(r)

	var err error
	r, err = captureRequestBody(r)
	if err != nil {
		log.Printf("proxy body capture error: %v", err)
		writeMaskedError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	r = attachCapturedBody(r)
	defer releaseCapturedBody(r)

	if err := p.runRequestPhase(w, r); err != nil {
		if errors.Is(err, errShortCircuited) {
			return
		}
		log.Printf("proxy request phase error: %v", err)
		writeMaskedError(w, http.StatusBadGateway, "upstream request failed")
		return
	}

	target, transport := p.snapshotTarget()
	upstreamReq, err := p.buildUpstreamRequest(r, target)
	if err != nil {
		log.Printf("proxy upstream build error: %v", err)
		writeMaskedError(w, http.StatusBadGateway, "upstream request failed")
		return
	}

	upstreamResp, err := transport.RoundTrip(upstreamReq)
	if err != nil {
		log.Printf("proxy upstream round trip error: %v", err)
		writeMaskedError(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	defer upstreamResp.Body.Close()

	if err := p.deliverResponse(w, r, upstreamResp); err != nil {
		log.Printf("proxy response delivery error: %v", err)
	}
}

var errShortCircuited = errors.New("request short-circuited")

func (p *Pipeline) runRequestPhase(w http.ResponseWriter, r *http.Request) error {
	reqChains, _ := p.snapshotChains()

	for _, chain := range reqChains {
		matcher, interceptors, tappers := chain.snapshot()
		if matcher != nil && !matcher.Matches(r) {
			continue
		}

		for _, tapper := range tappers {
			tapRequestAsync(r.Context(), r, tapper)
		}

		for _, interceptor := range interceptors {
			resp, err := interceptor.InterceptRequest(r)
			if err != nil {
				writeMaskedError(w, http.StatusInternalServerError, "interceptor failed")
				return errShortCircuited
			}
			if resp != nil {
				writeResponse(w, resp)
				return errShortCircuited
			}
		}
	}

	return nil
}

type responseChainMatch struct {
	interceptors []ResponseInterceptor
	tappers      []ResponseTapper
}

func (p *Pipeline) matchingResponseChains(clientReq *http.Request) []responseChainMatch {
	_, resChains := p.snapshotChains()
	var matches []responseChainMatch
	for _, chain := range resChains {
		matcher, interceptors, tappers := chain.snapshot()
		if matcher != nil && !matcher.Matches(clientReq) {
			continue
		}
		if len(interceptors) == 0 && len(tappers) == 0 {
			continue
		}
		matches = append(matches, responseChainMatch{
			interceptors: interceptors,
			tappers:      tappers,
		})
	}
	return matches
}

func (p *Pipeline) deliverResponse(w http.ResponseWriter, clientReq *http.Request, upstreamResp *http.Response) error {
	matches := p.matchingResponseChains(clientReq)

	hasInterceptors := false
	var tappers []ResponseTapper
	for _, m := range matches {
		if len(m.interceptors) > 0 {
			hasInterceptors = true
		}
		tappers = append(tappers, m.tappers...)
	}

	if hasInterceptors {
		bodyBytes, err := readAllPooled(upstreamResp.Body)
		if err != nil {
			return err
		}
		upstreamResp.Body.Close()
		upstreamResp.Body = bodyReader(bodyBytes)
		upstreamResp.ContentLength = int64(len(bodyBytes))

		for _, m := range matches {
			for _, interceptor := range m.interceptors {
				if err := interceptor.InterceptResponse(upstreamResp); err != nil {
					return err
				}
			}
		}

		if upstreamResp.Body != nil {
			bodyBytes, err = readAllPooled(upstreamResp.Body)
			if err != nil {
				return err
			}
			upstreamResp.Body.Close()
		}

		for _, tapper := range tappers {
			tapResponseBuffered(clientReq.Context(), upstreamResp.StatusCode, upstreamResp.Header, bodyBytes, tapper)
		}

		copyHeaders(w.Header(), upstreamResp.Header)
		w.WriteHeader(upstreamResp.StatusCode)
		_, err = w.Write(bodyBytes)
		return err
	}

	copyHeaders(w.Header(), upstreamResp.Header)
	w.WriteHeader(upstreamResp.StatusCode)

	if len(tappers) == 0 {
		_, err := io.Copy(w, upstreamResp.Body)
		return err
	}

	reader := io.Reader(upstreamResp.Body)
	var tapSinks []*asyncTapWriter
	for _, tapper := range tappers {
		sink := newAsyncTapWriter(defaultTapChannelDepth)
		tapSinks = append(tapSinks, sink)
		startAsyncResponseTap(clientReq.Context(), upstreamResp.StatusCode, upstreamResp.Header.Clone(), sink, tapper)
		reader = io.TeeReader(reader, sink)
	}

	err := copyResponseBody(w, reader)
	for _, sink := range tapSinks {
		_ = sink.Close()
	}
	return err
}

func copyResponseBody(w http.ResponseWriter, reader io.Reader) error {
	flusher, canFlush := w.(http.Flusher)
	buf := getBuffer()
	defer putBuffer(buf)
	if cap(*buf) < defaultBufferSize {
		*buf = make([]byte, defaultBufferSize)
	}
	slice := (*buf)[:defaultBufferSize]

	for {
		n, readErr := reader.Read(slice)
		if n > 0 {
			if _, writeErr := w.Write(slice[:n]); writeErr != nil {
				return writeErr
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

func tapRequestAsync(ctx context.Context, req *http.Request, tapper RequestTapper) {
	reqCopy := cloneRequestForTap(req)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("request tapper panic: %v", rec)
			}
		}()
		tapper.TapRequest(ctx, reqCopy)
	}()
}

func tapResponseBuffered(ctx context.Context, statusCode int, header http.Header, body []byte, tapper ResponseTapper) {
	h := header.Clone()
	b := body
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("response tapper panic: %v", rec)
			}
		}()
		tapper.TapResponse(ctx, statusCode, h, bodyReader(b))
	}()
}

func cloneRequestForTap(req *http.Request) *http.Request {
	ctx := snapshotMetadataContext(req.Context())
	clone := req.Clone(ctx)
	clone.Body = http.NoBody
	clone.GetBody = nil
	return clone
}

func (p *Pipeline) buildUpstreamRequest(r *http.Request, target *url.URL) (*http.Request, error) {
	upstreamURL := *target
	upstreamURL.Path = singleJoiningSlash(target.Path, r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	outReq := r.Clone(r.Context())
	outReq.URL = &upstreamURL
	outReq.Host = upstreamURL.Host
	outReq.RequestURI = ""

	outReq.Header = r.Header.Clone()
	outReq.Header.Del("Connection")
	outReq.Header.Del("Keep-Alive")
	outReq.Header.Del("Proxy-Authenticate")
	outReq.Header.Del("Proxy-Authorization")
	outReq.Header.Del("Te")
	outReq.Header.Del("Trailers")
	outReq.Header.Del("Transfer-Encoding")
	outReq.Header.Del("Upgrade")

	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}
	if prior := outReq.Header.Get("X-Forwarded-For"); prior != "" {
		clientIP = prior + ", " + clientIP
	}
	outReq.Header.Set("X-Forwarded-For", clientIP)
	outReq.Header.Set("X-Real-IP", clientIP)

	if payload, ok := capturedPayload(r); ok {
		outReq.Body = payload.reader()
		outReq.ContentLength = payload.len()
		outReq.GetBody = func() (io.ReadCloser, error) {
			return payload.reader(), nil
		}
		return outReq, nil
	}

	if outReq.Body != nil && outReq.Body != http.NoBody {
		payload, err := readBodyPooled(outReq.Body)
		if err != nil {
			return nil, err
		}
		outReq.Body.Close()
		outReq.Body = payload.reader()
		outReq.ContentLength = payload.len()
		outReq.GetBody = func() (io.ReadCloser, error) {
			return payload.reader(), nil
		}
	}

	return outReq, nil
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if b == "" {
			return a
		}
		return a + "/" + b
	default:
		return a + b
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func writeResponse(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if resp.Body != nil {
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	}
}

func writeMaskedError(w http.ResponseWriter, status int, publicMessage string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"`+publicMessage+`"}`)
}
