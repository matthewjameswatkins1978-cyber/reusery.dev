// Package httpx performs the bounded HTTP work every discovery provider
// needs, using only the standard library: context cancellation, a hard
// response-size ceiling, expected Content-Type handling, status-code
// validation, non-strict JSON decoding for external APIs, and error messages
// that never contain credentials.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Sentinel transport errors. They carry no response body, so a hostile or
// broken endpoint cannot push arbitrary text into Reusery's logs.
var (
	// ErrBodyTooLarge reports a response larger than the configured ceiling.
	ErrBodyTooLarge = errors.New("httpx: response body exceeds the configured limit")
	// ErrNotJSON reports a success response that is not JSON.
	ErrNotJSON = errors.New("httpx: response is not JSON")
	// ErrDecode reports a success response whose body is not the JSON shape
	// Reusery consumes.
	ErrDecode = errors.New("httpx: cannot decode response")
	// ErrBadBaseURL reports a base URL that is not an absolute http(s) URL.
	ErrBadBaseURL = errors.New("httpx: base URL is not an absolute http(s) URL")
	// ErrBadTarget reports a request target that would leave the base URL.
	ErrBadTarget = errors.New("httpx: request target must be a root-relative path")
	// ErrRedirectRefused reports a redirect that would leave the fixed base
	// host.
	ErrRedirectRefused = errors.New("httpx: refused redirect off the base host")
)

// StatusError reports a non-2xx response. Only safe fields are kept: no
// request headers, no authorization values and no response body.
type StatusError struct {
	StatusCode         int
	RetryAfter         string
	RateLimitRemaining string
	RateLimitReset     string
	host               string
}

func (e *StatusError) Error() string {
	message := fmt.Sprintf("httpx: %s responded HTTP %d", e.host, e.StatusCode)
	if e.RetryAfter != "" {
		message += fmt.Sprintf(" (retry-after=%s)", e.RetryAfter)
	}
	if e.RateLimitRemaining != "" {
		message += fmt.Sprintf(" (x-ratelimit-remaining=%s)", e.RateLimitRemaining)
	}
	return message
}

// Response is the safe subset of a response a provider needs to classify it.
type Response struct {
	StatusCode         int
	RetryAfter         string
	RateLimitRemaining string
	RateLimitReset     string
}

// Client issues bounded GETs against a single fixed, code-owned base URL.
type Client struct {
	rawBase          string
	host             string
	userAgent        string
	maxResponseBytes int64
	http             *http.Client
	invalid          error
}

// New builds a client for an absolute http(s) base URL. The URL is fixed at
// construction: providers never take a base URL from user configuration, which
// keeps discovery from turning into an SSRF-shaped feature. Any embedded
// credentials are dropped.
func New(baseURL, userAgent string, maxResponseBytes int64) *Client {
	client := &Client{
		userAgent:        userAgent,
		maxResponseBytes: maxResponseBytes,
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		client.invalid = ErrBadBaseURL
		return client
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	client.rawBase = strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host+parsed.EscapedPath(), "/")
	client.host = parsed.Host
	client.http = &http.Client{
		// Redirects are followed only inside the fixed base host.
		//
		// Canonicalising redirects are real: pkg.go.dev moved its JSON API
		// from /v1beta to /v1 and issues a permanent redirect, so refusing all
		// redirects would break against a cooperative, trusted endpoint. A
		// redirect to another host is still refused, so following one can never
		// turn a code-owned base URL into a way to reach somewhere else.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("httpx: too many redirects")
			}
			if req.URL.Host != client.host {
				return fmt.Errorf("%w: refused redirect to %s", ErrRedirectRefused, req.URL.Host)
			}
			return nil
		},
	}
	return client
}

// GetJSON performs one bounded GET and decodes the body into into.
//
// Decoding is deliberately non-strict: external APIs add fields, and only the
// fields Reusery consumes matter. Strict decoding remains correct for
// Reusery-authored YAML and JSON.
func (c *Client) GetJSON(ctx context.Context, path string, headers map[string]string, into any) (Response, error) {
	var response Response
	if c.invalid != nil {
		return response, c.invalid
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") {
		return response, ErrBadTarget
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rawBase+path, nil)
	if err != nil {
		return response, fmt.Errorf("httpx: build request: %w", err)
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	httpResponse, err := c.http.Do(request)
	if err != nil {
		return response, err
	}
	defer func() { _ = httpResponse.Body.Close() }()

	response = Response{
		StatusCode:         httpResponse.StatusCode,
		RetryAfter:         httpResponse.Header.Get("Retry-After"),
		RateLimitRemaining: httpResponse.Header.Get("X-RateLimit-Remaining"),
		RateLimitReset:     httpResponse.Header.Get("X-RateLimit-Reset"),
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return response, &StatusError{
			StatusCode:         response.StatusCode,
			RetryAfter:         response.RetryAfter,
			RateLimitRemaining: response.RateLimitRemaining,
			RateLimitReset:     response.RateLimitReset,
			host:               request.URL.Host,
		}
	}

	contentType := httpResponse.Header.Get("Content-Type")
	if !strings.Contains(contentType, "json") {
		return response, ErrNotJSON
	}

	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, c.maxResponseBytes+1))
	if err != nil {
		return response, fmt.Errorf("httpx: read response: %w", err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return response, ErrBodyTooLarge
	}
	if into == nil {
		return response, nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return response, fmt.Errorf("%w: %v", ErrDecode, err)
	}
	return response, nil
}
