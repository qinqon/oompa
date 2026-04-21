package agent

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

type cacheEntry struct {
	etag       string
	body       []byte
	header     http.Header
	statusCode int
}

type CachingTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	cache map[string]*cacheEntry
}

func NewCachingTransport(base http.RoundTripper) *CachingTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &CachingTransport{
		base:  base,
		cache: make(map[string]*cacheEntry),
	}
}

func (t *CachingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.base.RoundTrip(req)
	}

	key := req.URL.String()

	t.mu.Lock()
	entry, ok := t.cache[key]
	t.mu.Unlock()

	if ok {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", entry.etag)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusNotModified && ok {
		resp.Body.Close()
		return &http.Response{
			StatusCode: entry.statusCode,
			Header:     entry.header.Clone(),
			Body:       io.NopCloser(bytes.NewReader(entry.body)),
			Request:    req,
		}, nil
	}

	etag := resp.Header.Get("ETag")
	if resp.StatusCode == http.StatusOK && etag != "" {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		t.mu.Lock()
		t.cache[key] = &cacheEntry{
			etag:       etag,
			body:       body,
			header:     resp.Header.Clone(),
			statusCode: resp.StatusCode,
		}
		t.mu.Unlock()

		resp.Body = io.NopCloser(bytes.NewReader(body))
	}

	return resp, nil
}
