# ETag Caching Transport

Transparent HTTP caching layer that uses GitHub's `ETag` / `If-None-Match` conditional request headers. Responses returning `304 Not Modified` do not count against GitHub's rate limit.

Drives: `pkg/agent/etag.go` + `pkg/agent/etag_test.go`

## Types

```go
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
```

## Constructor

```go
func NewCachingTransport(base http.RoundTripper) *CachingTransport
```

Returns a new `CachingTransport` wrapping the given base transport. If `base` is `nil`, uses `http.DefaultTransport`.

## RoundTrip Logic

```go
func (t *CachingTransport) RoundTrip(req *http.Request) (*http.Response, error)
```

1. If `req.Method != "GET"`: delegate directly to `t.base.RoundTrip(req)`
2. Look up `t.cache[req.URL.String()]`
3. If entry exists: clone request, set `If-None-Match: <etag>` header
4. Call `t.base.RoundTrip(req)`
5. On error: return the error (do not use cache)
6. If response status is `304`:
   - Close the 304 body
   - Return a synthetic `*http.Response` with cached body, cached headers, status `200`, and the original request
7. If response status is `200` and response has an `ETag` header:
   - Read and store body, ETag, response headers, and status code in cache
   - Return a new `*http.Response` with body replaced by `io.NopCloser(bytes.NewReader(storedBody))`
8. Otherwise: return the response unchanged (do not cache non-200 or ETag-less responses)

## Cache Key

`req.URL.String()` — the full URL including query parameters. Only GET requests are cached.

## Thread Safety

`t.mu` protects all reads and writes to `t.cache`. The lock is held briefly for map lookups and stores, not during HTTP round-trips.

## Integration

Applied in both `GoGitHubClient` constructors (`github.go`):

- `NewGoGitHubClient(token)`: wraps `http.DefaultTransport`
- `NewGoGitHubClientFromHTTPClient(httpClient)`: wraps the client's existing transport

No changes to `GitHubClient` interface or method implementations.

## Tests (`etag_test.go`)

All tests use `httptest.Server` with a handler that tracks request count and returns ETags.

- `TestCachingTransport_FirstRequest` — GET returns 200 with ETag, body is correct, entry is cached
- `TestCachingTransport_CachedRequest_304` — second GET includes `If-None-Match`, server returns 304, transport returns cached 200 body
- `TestCachingTransport_CachedRequest_Changed` — server returns 200 with new ETag on second request, cache is updated
- `TestCachingTransport_NonGET_NotCached` — POST passes through without caching
- `TestCachingTransport_NoETag_NotCached` — response without ETag is not cached
- `TestCachingTransport_ConcurrentAccess` — multiple goroutines make requests without data races (use `go test -race`)
