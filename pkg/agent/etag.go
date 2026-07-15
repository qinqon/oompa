package agent

import (
	"bytes"
	"container/list"
	"io"
	"net/http"
	"sync"
)

const (
	// maxCacheEntries bounds the number of cached responses. The daemon runs
	// for weeks and every paginated URL is a distinct cache key, so an
	// unbounded map grows forever. Least-recently-used entries are evicted.
	maxCacheEntries = 512

	// maxCachedBodyBytes skips caching oversized response bodies (e.g. log
	// payloads) that would dominate memory for little conditional-GET win.
	maxCachedBodyBytes = 1 << 20 // 1 MiB
)

type cacheEntry struct {
	etag       string
	body       []byte
	header     http.Header
	statusCode int
	elem       *list.Element // position in the LRU order list; value is the cache key
}

type CachingTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	cache map[string]*cacheEntry
	order *list.List // LRU order: front = most recently used
}

func NewCachingTransport(base http.RoundTripper) *CachingTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &CachingTransport{
		base:  base,
		cache: make(map[string]*cacheEntry),
		order: list.New(),
	}
}

// touch marks a cached entry as most recently used. Callers must hold t.mu.
func (t *CachingTransport) touch(entry *cacheEntry) {
	t.order.MoveToFront(entry.elem)
}

// store inserts or updates a cache entry, evicting the least-recently-used
// entry when the cache is full. Callers must hold t.mu.
func (t *CachingTransport) store(key string, entry *cacheEntry) {
	if existing, ok := t.cache[key]; ok {
		entry.elem = existing.elem
		t.cache[key] = entry
		t.order.MoveToFront(entry.elem)
		return
	}
	entry.elem = t.order.PushFront(key)
	t.cache[key] = entry
	if len(t.cache) > maxCacheEntries {
		oldest := t.order.Back()
		if oldest != nil {
			t.order.Remove(oldest)
			delete(t.cache, oldest.Value.(string))
		}
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
		t.mu.Lock()
		t.touch(entry)
		t.mu.Unlock()
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

		if len(body) <= maxCachedBodyBytes {
			t.mu.Lock()
			t.store(key, &cacheEntry{
				etag:       etag,
				body:       body,
				header:     resp.Header.Clone(),
				statusCode: resp.StatusCode,
			})
			t.mu.Unlock()
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
	}

	return resp, nil
}
