package agent

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCachingTransport_FirstRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte(`{"data":"hello"}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body) //nolint:errcheck // test helper
	if string(body) != `{"data":"hello"}` {
		t.Fatalf("unexpected body: %s", body)
	}

	transport.mu.Lock()
	entry, ok := transport.cache[server.URL+"/test"]
	transport.mu.Unlock()
	if !ok {
		t.Fatal("expected cache entry")
	}
	if entry.etag != `"abc123"` {
		t.Fatalf("expected etag %q, got %q", `"abc123"`, entry.etag)
	}
}

func TestCachingTransport_CachedRequest_304(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Header.Get("If-None-Match") == `"abc123"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte(`{"data":"hello"}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	resp, err = client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"data":"hello"}` {
		t.Fatalf("expected cached body, got: %s", body)
	}

	if requestCount.Load() != 2 {
		t.Fatalf("expected 2 server requests, got %d", requestCount.Load())
	}
}

func TestCachingTransport_CachedRequest_Changed(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"version":1}`))
			return
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write([]byte(`{"version":2}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	resp, err = client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"version":2}` {
		t.Fatalf("expected updated body, got: %s", body)
	}

	transport.mu.Lock()
	entry := transport.cache[server.URL+"/test"]
	transport.mu.Unlock()
	if entry.etag != `"v2"` {
		t.Fatalf("expected updated etag %q, got %q", `"v2"`, entry.etag)
	}
}

func TestCachingTransport_NonGET_NotCached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/test", http.NoBody)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	transport.mu.Lock()
	_, ok := transport.cache[server.URL+"/test"]
	transport.mu.Unlock()
	if ok {
		t.Fatal("POST response should not be cached")
	}
}

func TestCachingTransport_NoETag_NotCached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":"no-etag"}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	transport.mu.Lock()
	_, ok := transport.cache[server.URL+"/test"]
	transport.mu.Unlock()
	if ok {
		t.Fatal("response without ETag should not be cached")
	}
}

func TestCachingTransport_ConcurrentAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"concurrent"`)
		_, _ = w.Write([]byte(`{"data":"concurrent"}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	// Seed the cache
	resp, err := client.Get(server.URL + "/test")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			resp, err := client.Get(server.URL + "/test")
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("expected 200, got %d", resp.StatusCode)
			}
			body, _ := io.ReadAll(resp.Body)
			if string(body) != `{"data":"concurrent"}` {
				t.Errorf("unexpected body: %s", body)
			}
		})
	}
	wg.Wait()
}

func TestCachingTransport_EvictsLeastRecentlyUsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"e-`+r.URL.Path+`"`)
		_, _ = w.Write([]byte(`{"path":"` + r.URL.Path + `"}`))
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	get := func(path string) {
		resp, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	// Fill the cache to capacity, then touch the first entry to make it
	// recently used, and insert one more to force an eviction.
	for i := range maxCacheEntries {
		get(fmt.Sprintf("/item-%d", i))
	}
	get("/item-0") // touch: /item-0 becomes most recently used
	get("/one-more")

	transport.mu.Lock()
	size := len(transport.cache)
	_, item0Alive := transport.cache[server.URL+"/item-0"]
	_, item1Alive := transport.cache[server.URL+"/item-1"]
	transport.mu.Unlock()

	if size != maxCacheEntries {
		t.Fatalf("expected cache bounded at %d entries, got %d", maxCacheEntries, size)
	}
	if !item0Alive {
		t.Error("expected recently-touched /item-0 to survive eviction")
	}
	if item1Alive {
		t.Error("expected least-recently-used /item-1 to be evicted")
	}
}

func TestCachingTransport_OversizedBodyNotCached(t *testing.T) {
	big := bytes.Repeat([]byte("x"), maxCachedBodyBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"big"`)
		_, _ = w.Write(big)
	}))
	defer server.Close()

	transport := NewCachingTransport(http.DefaultTransport)
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if len(body) != len(big) {
		t.Fatalf("expected full %d-byte body passthrough, got %d", len(big), len(body))
	}

	transport.mu.Lock()
	_, ok := transport.cache[server.URL+"/big"]
	transport.mu.Unlock()
	if ok {
		t.Fatal("oversized body should not be cached")
	}
}
