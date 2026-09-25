package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testUserAgent = "reusery-discovery (+https://reusery.dev)"

func TestGetJSONDecodesExternalFieldsWithoutStrictness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"items":[{"path":"a/b"}],"total":1,"brand_new_field":{"nested":true}}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	var payload struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
		Total int `json:"total"`
	}
	response, err := client.GetJSON(context.Background(), "/v1/search?q=thing&limit=5", nil, &payload)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if response.StatusCode != http.StatusOK || payload.Total != 1 || len(payload.Items) != 1 {
		t.Errorf("response = %#v payload = %#v", response, payload)
	}
}

func TestContextCancellationStopsWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.GetJSON(ctx, "/slow", nil, &map[string]any{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a context deadline failure", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("request ran for %v, want cancellation to stop it promptly", elapsed)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	const limit = 2 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"blob":"` + strings.Repeat("a", limit) + `"}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, limit)
	_, err := client.GetJSON(context.Background(), "/big", nil, &map[string]any{})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("error = %v, want ErrBodyTooLarge", err)
	}
}

func TestNonJSONBodiesAreRejected(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantErr     error
	}{
		{"html content type", "text/html", "<html>login</html>", ErrNotJSON},
		{"plain text content type", "text/plain", `{"items":[]}`, ErrNotJSON},
		{"no content type", "", `{"items":[]}`, ErrNotJSON},
		{"json content type with broken body", "application/json", `{"items": not json`, ErrDecode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			client := New(server.URL, testUserAgent, 4096)
			_, err := client.GetJSON(context.Background(), "/x", nil, &map[string]any{})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStatusErrorCarriesSafeFieldsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded","authorization":"Bearer leaked"}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	_, err := client.GetJSON(context.Background(), "/limited?q=thing", nil, &map[string]any{})

	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v, want *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d", statusErr.StatusCode)
	}
	if statusErr.RetryAfter != "42" || statusErr.RateLimitRemaining != "0" {
		t.Errorf("headers = %#v", statusErr)
	}
	if strings.Contains(err.Error(), "leaked") || strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error echoed the response body: %q", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want the status code", err)
	}
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	var secondHostHits int
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHostHits++
	}))
	t.Cleanup(second.Close)

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL+"/elsewhere", http.StatusFound)
	}))
	t.Cleanup(first.Close)

	client := New(first.URL, testUserAgent, 4096)
	_, err := client.GetJSON(context.Background(), "/move", nil, &map[string]any{})

	if !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("error = %v, want ErrRedirectRefused", err)
	}
	if secondHostHits != 0 {
		t.Errorf("redirect was followed to another host (%d hits); base URLs must stay fixed", secondHostHits)
	}
}

func TestSameHostRedirectIsFollowed(t *testing.T) {
	// A cooperative endpoint may canonicalise its own path. pkg.go.dev, for
	// example, permanently redirects /v1beta to /v1.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1beta/search" {
			http.Redirect(w, r, "/v1/search?"+r.URL.RawQuery, http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"path":"a/b"}],"total":1}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	var payload struct {
		Total int `json:"total"`
	}
	if _, err := client.GetJSON(context.Background(), "/v1beta/search?q=x", nil, &payload); err != nil {
		t.Fatalf("GetJSON across a same-host redirect: %v", err)
	}
	if payload.Total != 1 {
		t.Errorf("total = %d, want the canonical response", payload.Total)
	}
}

func TestRequestTargetMustStayUnderTheBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	if _, err := client.GetJSON(context.Background(), "https://elsewhere.test/steal", nil, &map[string]any{}); !errors.Is(err, ErrBadTarget) {
		t.Errorf("absolute target error = %v, want ErrBadTarget", err)
	}
	if _, err := client.GetJSON(context.Background(), "relative", nil, &map[string]any{}); !errors.Is(err, ErrBadTarget) {
		t.Errorf("relative target error = %v, want ErrBadTarget", err)
	}
}

func TestInvalidBaseURLsAreRefused(t *testing.T) {
	for _, baseURL := range []string{"", "not-a-url", "ftp://example.test", "example.test"} {
		client := New(baseURL, testUserAgent, 4096)
		if _, err := client.GetJSON(context.Background(), "/x", nil, &map[string]any{}); !errors.Is(err, ErrBadBaseURL) {
			t.Errorf("base %q error = %v, want ErrBadBaseURL", baseURL, err)
		}
	}
}

func TestCredentialsInBaseURLAreDropped(t *testing.T) {
	var sawBasicAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.User != nil || r.Header.Get("Authorization") != "" {
			sawBasicAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := New("http://user:secret@"+strings.TrimPrefix(server.URL, "http://"), testUserAgent, 4096)
	if _, err := client.GetJSON(context.Background(), "/x", nil, &map[string]any{}); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if sawBasicAuth {
		t.Error("credentials embedded in a base URL were sent to the server")
	}
}

func TestQueriesNeverCarryCredentials(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := New(server.URL, testUserAgent, 4096)
	headers := map[string]string{"Authorization": "Bearer secret-token"}
	if _, err := client.GetJSON(context.Background(), "/search?q=subprocess+cancellation&limit=5", headers, &map[string]any{}); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if seenQuery != "q=subprocess+cancellation&limit=5" {
		t.Errorf("query = %q, want only the provider query parameters", seenQuery)
	}
	if strings.Contains(seenQuery, "secret-token") {
		t.Error("a credential reached the URL")
	}
}
