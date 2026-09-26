package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
)

const testKey = "sk-test-absolute-secret-key-do-not-leak"

// capturedRequest records what the adapter actually sent.
type capturedRequest struct {
	Path   string
	Header http.Header
	Body   map[string]any
	Raw    string
}

func testRequest() intent.ProviderRequest {
	return intent.ProviderRequest{
		Instructions:    "Structure the intent.",
		Input:           "run child processes",
		JSONSchema:      intent.JSONSchema(),
		SchemaName:      intent.SchemaName,
		MaxOutputTokens: 2500,
	}
}

// serve builds a provider bound to an httptest endpoint. The endpoint is
// injected through the unexported constructor, so there is no runtime
// configuration for a base URL in production.
func serve(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Provider, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		captured.Raw = string(raw)
		_ = json.Unmarshal(raw, &captured.Body)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return newProvider(testKey, "gpt-6-luna", server.URL+"/v1/responses", server.Client()), captured
}

// completedEnvelope is a realistic Responses API payload.
func completedEnvelope(text string) string {
	return `{
		"id": "resp_abc123",
		"object": "response",
		"status": "completed",
		"model": "gpt-6-luna",
		"output": [
			{"type": "reasoning", "id": "rs_1", "summary": []},
			{"type": "message", "id": "msg_1", "role": "assistant",
			 "content": [{"type": "output_text", "annotations": [], "text": ` + jsonString(text) + `}]}
		],
		"usage": {
			"input_tokens": 42,
			"output_tokens": 17,
			"total_tokens": 59,
			"output_tokens_details": {"reasoning_tokens": 6}
		}
	}`
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestIDAndProductionEndpointAreCodeOwned(t *testing.T) {
	if ID != "openai" {
		t.Errorf("ID = %q", ID)
	}
	if Endpoint != "https://api.openai.com/v1/responses" {
		t.Errorf("Endpoint = %q, want the code-owned Responses API URL", Endpoint)
	}
	provider, err := New(testKey, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if provider.endpoint != Endpoint {
		t.Errorf("production provider endpoint = %q, want %q", provider.endpoint, Endpoint)
	}
	if provider.model != DefaultModel {
		t.Errorf("model = %q, want the default", provider.model)
	}
	if provider.ID() != "openai" {
		t.Errorf("provider ID = %q", provider.ID())
	}
}

func TestNewRejectsAMissingKeyWithoutEchoingAnything(t *testing.T) {
	for _, key := range []string{"", "   "} {
		provider, err := New(key, "gpt-6-luna")
		if !errors.Is(err, ErrMissingKey) {
			t.Errorf("New(%q) err = %v, want ErrMissingKey", key, err)
		}
		if provider != nil {
			t.Errorf("New(%q) returned a provider", key)
		}
	}
	if strings.Contains(ErrMissingKey.Error(), testKey) {
		t.Error("the missing-key error leaks a key")
	}
}

func TestNewDoesNotExposeARuntimeBaseURLConfiguration(t *testing.T) {
	// New takes a key and a model. Nothing else may redirect production
	// traffic away from the code-owned endpoint; the httptest injection used
	// by this file goes through the unexported newProvider.
	constructor := reflect.TypeOf(New)
	if constructor.NumIn() != 2 || constructor.NumOut() != 2 {
		t.Errorf("New has %d inputs and %d outputs, want 2 and 2",
			constructor.NumIn(), constructor.NumOut())
	}
	if constructor.In(0).Kind() != reflect.String || constructor.In(1).Kind() != reflect.String {
		t.Error("New must take the API key and the model name")
	}

	provider, err := New(testKey, "some-model")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if provider.endpoint != Endpoint {
		t.Errorf("endpoint = %q, want the code-owned %q", provider.endpoint, Endpoint)
	}
}

func TestGenerateSendsTheBoundedRequestShape(t *testing.T) {
	provider, captured := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, completedEnvelope(`{"status":"ready"}`))
	})

	response, err := provider.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if captured.Path != "/v1/responses" {
		t.Errorf("path = %q, want /v1/responses", captured.Path)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("authorization header = %q, want a bearer token", got)
	}
	if captured.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content type = %q", captured.Header.Get("Content-Type"))
	}

	body := captured.Body
	if body["model"] != "gpt-6-luna" {
		t.Errorf("model = %v", body["model"])
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false (no server-side conversation state)", body["store"])
	}
	if _, present := body["tools"]; present {
		t.Error("tools must not be supplied at all")
	}
	if _, present := body["previous_response_id"]; present {
		t.Error("previous_response_id must never be sent")
	}
	if _, present := body["conversation"]; present {
		t.Error("conversation must never be sent")
	}
	if body["max_output_tokens"] != float64(2500) {
		t.Errorf("max_output_tokens = %v, want 2500", body["max_output_tokens"])
	}

	if ReasoningEffort != "low" {
		t.Errorf("ReasoningEffort = %q, want low", ReasoningEffort)
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != ReasoningEffort {
		t.Errorf("reasoning = %v, want effort %q", reasoning, ReasoningEffort)
	}

	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format == nil {
		t.Fatalf("text.format missing: %v", body["text"])
	}
	if format["type"] != "json_schema" {
		t.Errorf("format type = %v, want json_schema", format["type"])
	}
	if format["strict"] != true {
		t.Errorf("strict = %v, want true", format["strict"])
	}
	if format["name"] != intent.SchemaName {
		t.Errorf("schema name = %v, want %q", format["name"], intent.SchemaName)
	}
	if _, present := format["schema"]; !present {
		t.Error("schema is missing")
	}

	if body["instructions"] != "Structure the intent." {
		t.Errorf("instructions = %v", body["instructions"])
	}
	if body["input"] != "run child processes" {
		t.Errorf("input = %v, want the user text passed as input, not instructions", body["input"])
	}

	// The schema sent on the wire is the Packet 6 authored schema, which is
	// what makes the recorded schema version meaningful.
	schema, _ := format["schema"].(map[string]any)
	if schema == nil {
		t.Fatal("schema did not decode as an object")
	}
	if _, present := schema["properties"]; !present {
		t.Error("schema has no properties")
	}
	wireSchema, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("re-encode schema: %v", err)
	}
	var authored map[string]any
	if err := json.Unmarshal([]byte(intent.JSONSchema()), &authored); err != nil {
		t.Fatalf("authored schema: %v", err)
	}
	authoredSchema, err := json.Marshal(authored)
	if err != nil {
		t.Fatalf("re-encode authored schema: %v", err)
	}
	if string(wireSchema) != string(authoredSchema) {
		t.Error("wire schema differs from the versioned Packet 6 schema")
	}
	if intent.SchemaVersion != 1 {
		t.Errorf("schema version = %d, want 1", intent.SchemaVersion)
	}

	if response.ProviderID != "openai" {
		t.Errorf("provider id = %q", response.ProviderID)
	}
	if response.Status != intent.ResponseCompleted {
		t.Errorf("status = %q", response.Status)
	}
}

func TestGenerateRecordsModelResponseIDAndUsage(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, completedEnvelope(`{"status":"ready","capability":"x"}`))
	})

	response, err := provider.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response.StructuredJSON != `{"status":"ready","capability":"x"}` {
		t.Errorf("structured output = %q", response.StructuredJSON)
	}
	if response.Model != "gpt-6-luna" {
		t.Errorf("model = %q", response.Model)
	}
	if response.ResponseID != "resp_abc123" {
		t.Errorf("response id = %q", response.ResponseID)
	}
	want := intent.Usage{InputTokens: 42, OutputTokens: 17, ReasoningTokens: 6, TotalTokens: 59}
	if response.Usage != want {
		t.Errorf("usage = %#v, want %#v", response.Usage, want)
	}
}

func TestGenerateFallsBackToTheConfiguredModelAndToleratesUnknownFields(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{
			"status": "completed",
			"something_new": {"nested": [1,2,3]},
			"output": [{"type":"message","content":[{"type":"output_text","text":"{}"}]}]
		}`)
	})
	response, err := provider.Generate(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if response.Model != "gpt-6-luna" {
		t.Errorf("model = %q, want the configured model when the payload omits it", response.Model)
	}
}

func TestGenerateClassifiesHTTPFailures(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		header map[string]string
		want   intent.ErrorKind
	}{
		"unauthorized":    {status: http.StatusUnauthorized, body: `{"error":{"code":"invalid_api_key","message":"Incorrect API key provided: ` + testKey + `"}}`, want: intent.ErrorAuthentication},
		"forbidden":       {status: http.StatusForbidden, body: `{}`, want: intent.ErrorAuthentication},
		"rate limited":    {status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "60"}, body: `{"error":{"code":"rate_limit_exceeded"}}`, want: intent.ErrorRateLimited},
		"server error":    {status: http.StatusInternalServerError, body: `<html>boom</html>`, want: intent.ErrorUnavailable},
		"bad gateway":     {status: http.StatusBadGateway, body: ``, want: intent.ErrorUnavailable},
		"request timeout": {status: http.StatusRequestTimeout, body: ``, want: intent.ErrorTimeout},
		"bad request":     {status: http.StatusBadRequest, body: `{"error":{"code":"invalid_schema","message":"schema is not supported"}}`, want: intent.ErrorUnavailable},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range testCase.header {
					w.Header().Set(key, value)
				}
				writeJSON(w, testCase.status, testCase.body)
			})
			_, err := provider.Generate(context.Background(), testRequest())

			var providerError *intent.ProviderError
			if !errors.As(err, &providerError) {
				t.Fatalf("err = %v, want a classified provider error", err)
			}
			if providerError.Kind != testCase.want {
				t.Errorf("kind = %q, want %q", providerError.Kind, testCase.want)
			}
			if providerError.StatusCode != testCase.status {
				t.Errorf("status = %d, want %d", providerError.StatusCode, testCase.status)
			}
			if !errors.Is(err, intent.ErrProvider) {
				t.Errorf("err is not behind ErrProvider: %v", err)
			}
			// Nothing may echo a credential, a header or a raw body.
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("error leaks the API key: %v", err)
			}
			if strings.Contains(err.Error(), "Authorization") {
				t.Errorf("error leaks an authorization header: %v", err)
			}
		})
	}
}

func TestGenerateNeverEchoesProviderDetailForAuthFailures(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusUnauthorized,
			`{"error":{"code":"invalid_api_key","message":"Incorrect API key provided: `+testKey+`"}}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("auth failure echoed the provider message: %v", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("auth failure leaked the key: %v", err)
	}
}

func TestGenerateIncludesSafeRequestContractDetail(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest,
			`{"error":{"code":"invalid_schema","message":"`+strings.Repeat("x", 900)+`"}}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid_schema") {
		t.Errorf("error does not carry the provider code: %v", err)
	}
	if len(err.Error()) > 600 {
		t.Errorf("error is %d bytes; provider detail must stay bounded", len(err.Error()))
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("error contains a newline: %v", err)
	}
}

func TestGenerateClassifiesContextDeadlineAsTimeout(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		writeJSON(w, http.StatusOK, completedEnvelope(`{}`))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := provider.Generate(ctx, testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("timeout error leaks the key: %v", err)
	}
}

func TestGenerateClassifiesRefusal(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{
			"id": "resp_ref",
			"status": "completed",
			"output": [{"type": "message", "role": "assistant",
				"content": [{"type": "refusal", "refusal": "I cannot help with that."}]}]
		}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorRefused {
		t.Fatalf("err = %v, want refused", err)
	}
}

func TestGenerateClassifiesIncomplete(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{
			"id": "resp_inc",
			"status": "incomplete",
			"incomplete_details": {"reason": "max_output_tokens"},
			"output": []
		}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorIncomplete {
		t.Fatalf("err = %v, want incomplete", err)
	}
	if !strings.Contains(err.Error(), "max_output_tokens") {
		t.Errorf("error does not carry the incompleteness reason: %v", err)
	}
}

func TestGenerateClassifiesFailedStatus(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{"id":"resp_f","status":"failed","output":[]}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestGenerateClassifiesMissingStructuredText(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, `{
			"id": "resp_empty",
			"status": "completed",
			"output": [{"type": "message", "role": "assistant", "content": []}]
		}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorInvalidResponse {
		t.Fatalf("err = %v, want invalid_response", err)
	}
}

func TestGenerateClassifiesMalformedStructuredJSON(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, completedEnvelope("this is not json"))
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorInvalidResponse {
		t.Fatalf("err = %v, want invalid_response", err)
	}
}

func TestGenerateClassifiesAnUnreadableEnvelope(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, "not json at all")
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorInvalidResponse {
		t.Fatalf("err = %v, want invalid_response", err)
	}
}

func TestGenerateRejectsAnOversizedResponse(t *testing.T) {
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"blob":"`+strings.Repeat("a", maxResponseBytes+1024)+`"}`)
	})
	_, err := provider.Generate(context.Background(), testRequest())
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorInvalidResponse {
		t.Fatalf("err = %v, want invalid_response", err)
	}
}

func TestGenerateRejectsAnInvalidSchemaBeforeTheNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)
	provider := newProvider(testKey, "gpt-6-luna", server.URL, server.Client())

	request := testRequest()
	request.JSONSchema = "{not json"
	_, err := provider.Generate(context.Background(), request)
	var providerError *intent.ProviderError
	if !errors.As(err, &providerError) || providerError.Kind != intent.ErrorInvalidResponse {
		t.Fatalf("err = %v, want invalid_response", err)
	}
	if called {
		t.Error("an invalid schema must not reach the network")
	}
}

func TestGenerateNeverRetries(t *testing.T) {
	requests := 0
	provider, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeJSON(w, http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded"}}`)
	})
	for i := 0; i < 3; i++ {
		if _, err := provider.Generate(context.Background(), testRequest()); err == nil {
			t.Fatal("expected an error")
		}
	}
	if requests != 3 {
		t.Errorf("requests = %d, want exactly one per Generate (no hidden retries)", requests)
	}
}

func TestEncodeBoundsMaxOutputTokensToThePacketDefault(t *testing.T) {
	provider, captured := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, completedEnvelope(`{}`))
	})
	request := testRequest()
	request.MaxOutputTokens = 0
	if _, err := provider.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if captured.Body["max_output_tokens"] != float64(intent.DefaultBounds().MaxOutputTokens) {
		t.Errorf("max_output_tokens = %v, want the packet default", captured.Body["max_output_tokens"])
	}
}

func TestEncodeRejectsASchemaThatIsNotJSON(t *testing.T) {
	provider := newProvider(testKey, DefaultModel, "http://example.invalid/v1/responses",
		&http.Client{Timeout: time.Second})
	request := testRequest()
	request.JSONSchema = ""
	if _, err := provider.Generate(context.Background(), request); err == nil {
		t.Fatal("expected an error for an empty schema")
	}
}
