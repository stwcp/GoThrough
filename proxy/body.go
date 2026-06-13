package proxy

import (
	"context"
	"io"
	"net/http"
)

const capturedBodyKey contextKey = "proxy-captured-body"

func captureRequestBody(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}
	if _, ok := req.Context().Value(capturedBodyKey).(*pooledPayload); ok {
		return attachCapturedBody(req), nil
	}

	payload, err := readBodyPooled(req.Body)
	if err != nil {
		return req, err
	}
	if err := req.Body.Close(); err != nil {
		payload.release()
		return req, err
	}

	ctx := context.WithValue(req.Context(), capturedBodyKey, payload)
	req = req.WithContext(ctx)
	return attachCapturedBody(req), nil
}

func attachCapturedBody(req *http.Request) *http.Request {
	payload, ok := req.Context().Value(capturedBodyKey).(*pooledPayload)
	if !ok {
		return req
	}
	req.Body = payload.reader()
	req.ContentLength = payload.len()
	req.GetBody = func() (io.ReadCloser, error) {
		return payload.reader(), nil
	}
	return req
}

func capturedPayload(req *http.Request) (*pooledPayload, bool) {
	payload, ok := req.Context().Value(capturedBodyKey).(*pooledPayload)
	return payload, ok
}

func releaseCapturedBody(req *http.Request) {
	payload, ok := capturedPayload(req)
	if !ok {
		return
	}
	payload.release()
}