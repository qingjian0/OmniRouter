package websession

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// HTTPClient pins outbound web-session requests to one vendor origin and an
// explicit set of path prefixes. It deliberately does not follow redirects:
// credentials must never be forwarded to an unexpected origin.
type HTTPClient struct {
	origin   *url.URL
	prefixes []string
	client   *http.Client
}

func NewHTTPClient(origin string, allowedPathPrefixes ...string) (*HTTPClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("web-session origin must be an absolute HTTP(S) origin without credentials, path, query, or fragment")
	}
	if len(allowedPathPrefixes) == 0 {
		return nil, fmt.Errorf("at least one vendor API path prefix is required")
	}
	prefixes := make([]string, 0, len(allowedPathPrefixes))
	for _, prefix := range allowedPathPrefixes {
		prefix = strings.TrimSpace(prefix)
		if !strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, "//") ||
			path.Clean(prefix) != prefix || strings.ContainsAny(prefix, "?#\\") {
			return nil, fmt.Errorf("invalid vendor API path prefix")
		}
		prefixes = append(prefixes, strings.TrimSuffix(prefix, "/"))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	client := &http.Client{
		Transport: transport,
		// No total timeout: streaming duration is governed by the inbound
		// request context and the provider's own maximum stream policy.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &HTTPClient{origin: parsed, prefixes: prefixes, client: client}, nil
}

// Do sends a request to a validated path on the configured origin. The caller
// owns and must close the returned response body. Context cancellation is
// propagated directly to the upstream request.
func (c *HTTPClient) Do(ctx context.Context, method, requestPath string, headers http.Header, body io.Reader) (*http.Response, error) {
	if c == nil || c.client == nil || c.origin == nil {
		return nil, fmt.Errorf("web-session HTTP client is not initialized")
	}
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut && method != http.MethodDelete {
		return nil, fmt.Errorf("web-session HTTP method is not allowed")
	}
	parsedPath, err := url.ParseRequestURI(requestPath)
	if err != nil || parsedPath.IsAbs() || parsedPath.Host != "" || !strings.HasPrefix(parsedPath.Path, "/") ||
		strings.HasPrefix(parsedPath.Path, "//") || strings.Contains(parsedPath.Path, "\\") {
		return nil, fmt.Errorf("web-session request path is invalid")
	}
	decodedPath, err := url.PathUnescape(parsedPath.EscapedPath())
	if err != nil || !strings.HasPrefix(decodedPath, "/") || strings.HasPrefix(decodedPath, "//") ||
		path.Clean(decodedPath) != decodedPath || strings.Contains(decodedPath, "\\") {
		return nil, fmt.Errorf("web-session request path is invalid")
	}
	allowed := false
	for _, prefix := range c.prefixes {
		if decodedPath == prefix || strings.HasPrefix(decodedPath, prefix+"/") {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("web-session request path is outside the configured vendor API prefixes")
	}
	target := *c.origin
	target.Path = decodedPath
	target.RawQuery = parsedPath.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build web-session request: %w", err)
	}
	for name, values := range headers {
		switch strings.ToLower(name) {
		case "host", "connection", "proxy-authorization", "proxy-authenticate", "transfer-encoding", "upgrade":
			return nil, fmt.Errorf("hop-by-hop or routing header %q is not allowed", name)
		}
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return c.client.Do(req)
}

// ReadBoundedBody reads a non-streaming upstream response with a hard byte cap.
func ReadBoundedBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if body == nil {
		return nil, fmt.Errorf("web-session response body is required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("web-session response body limit must be positive")
	}
	value, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read web-session response: %w", err)
	}
	if int64(len(value)) > maxBytes {
		return nil, fmt.Errorf("web-session response exceeds %d bytes", maxBytes)
	}
	return value, nil
}
