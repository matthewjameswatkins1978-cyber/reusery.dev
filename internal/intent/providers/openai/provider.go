// Package openai adapts Reusery's provider-independent intent boundary to the
// OpenAI Responses API.
//
// This package owns HTTP, authentication, request payload construction,
// response decoding, provider error classification and usage extraction. It
// contains no Reusery product semantics: it never sees a Primitive, a
// Contract, a requirement, a validation message or an identifier beyond the
// opaque strings it is handed.
//
// The production endpoint is a code-owned constant. Tests inject an httptest
// URL through an unexported constructor, so there is deliberately no runtime
// configuration for a base URL.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
)

// Identifier and fixed production defaults.
const (
	// ID is the provider identity recorded in generation metadata.
	ID = "openai"
	// Endpoint is the code-owned Responses API URL. It is not configurable.
	Endpoint = "https://api.openai.com/v1/responses"
	// DefaultModel is the Packet 6 default: intent normalisation is bounded
	// structured work and Reusery has a first-class cost-saving objective.
	// A stronger model is not automatically the better default.
	DefaultModel = "gpt-5.6-luna"
	// ReasoningEffort is fixed at low for the same reason.
	ReasoningEffort = "low"

	defaultTimeout       = 20 * time.Second
	maxResponseBytes     = 1 << 20 // 1 MiB
	maxProviderDetailLen = 400
)

// ErrMissingKey reports an absent API key. The key itself is never included.
var ErrMissingKey = fmt.Errorf("intent: %s is required", "REUSERY_OPENAI_API_KEY")

// Provider issues bounded Responses API calls for one API key.
type Provider struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

// New builds the production provider. The endpoint is code-owned; only the
// key and the model come from configuration.
func New(apiKey, model string) (*Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, ErrMissingKey
	}
	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = DefaultModel
	}
	return newProvider(apiKey, resolvedModel, Endpoint, &http.Client{Timeout: defaultTimeout}), nil
}

// newProvider injects an endpoint and a client for tests only.
func newProvider(apiKey, model, endpoint string, client *http.Client) *Provider {
	return &Provider{
		apiKey:   strings.TrimSpace(apiKey),
		model:    model,
		endpoint: endpoint,
		client:   client,
	}
}

// ID reports the provider identity.
func (p *Provider) ID() string { return ID }

// reasoning mirrors the Responses API reasoning options.
type reasoning struct {
	Effort string `json:"effort"`
}

// formatSpec is the strict structured-output format. It is the only output
// shape Reusery requests: no free text, no tools, no sampling freedom.
type formatSpec struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

// textOptions carries the structured-output format.
type textOptions struct {
	Format formatSpec `json:"format"`
}

// requestBody is the Responses API payload Reusery sends.
//
// There is no `tools` field at all: no web search, no file search, no computer
// use, no functions, no MCP and no background mode. `store` is always false so
// the provider keeps no server-side conversation state, and no
// previous_response_id or conversation is ever sent — each normalisation is
// self-contained.
type requestBody struct {
	Model           string      `json:"model"`
	Store           bool        `json:"store"`
	Instructions    string      `json:"instructions"`
	Input           string      `json:"input"`
	MaxOutputTokens int         `json:"max_output_tokens"`
	Reasoning       reasoning   `json:"reasoning"`
	Text            textOptions `json:"text"`
}

// usageDetails is the token accounting Reusery consumes. Raw counts only:
// no monetary cost is calculated, because pricing changes and token usage is
// the durable evidence.
type usageDetails struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// envelope is the safe subset of a Responses API payload Reusery consumes.
// Unknown fields are tolerated: providers add fields, and only the fields
// Reusery reads matter.
type envelope struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Refusal string `json:"refusal"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Usage *usageDetails `json:"usage"`
}

// Generate performs exactly one bounded Responses API call. It never retries:
// a hidden retry would be another paid model call and could amplify an outage.
func (p *Provider) Generate(ctx context.Context, request intent.ProviderRequest) (intent.ProviderResponse, error) {
	payload, err := p.encode(request)
	if err != nil {
		return intent.ProviderResponse{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return intent.ProviderResponse{}, intent.NewProviderError(intent.ErrorUnavailable, 0, "cannot build request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		return intent.ProviderResponse{}, transportError(ctx, err)
	}
	defer func() { _ = httpResponse.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorUnavailable, httpResponse.StatusCode, "cannot read response body")
	}
	if len(body) > maxResponseBytes {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorInvalidResponse, httpResponse.StatusCode, "response body exceeds 1 MiB")
	}

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return intent.ProviderResponse{}, statusError(httpResponse.StatusCode, body)
	}

	var parsed envelope
	if err := json.Unmarshal(body, &parsed); err != nil {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorInvalidResponse, httpResponse.StatusCode, "response is not the expected shape")
	}

	if parsed.Error != nil {
		return intent.ProviderResponse{}, intent.NewProviderError(
			classifyCode(parsed.Error.Code), httpResponse.StatusCode, safeCode(parsed.Error.Code))
	}

	text, refused := extractText(&parsed)
	if refused {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorRefused, httpResponse.StatusCode, "model refused the request")
	}

	switch parsed.Status {
	case "", "completed":
	case "incomplete":
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorIncomplete, httpResponse.StatusCode, incompleteReason(&parsed))
	default:
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorUnavailable, httpResponse.StatusCode, "provider status "+safeStatus(parsed.Status))
	}

	if strings.TrimSpace(text) == "" {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorInvalidResponse, httpResponse.StatusCode, "response contains no structured output")
	}
	if !json.Valid([]byte(text)) {
		return intent.ProviderResponse{}, intent.NewProviderError(
			intent.ErrorInvalidResponse, httpResponse.StatusCode, "structured output is not valid JSON")
	}

	model := parsed.Model
	if model == "" {
		model = p.model
	}
	return intent.ProviderResponse{
		StructuredJSON: text,
		ProviderID:     ID,
		Model:          model,
		ResponseID:     parsed.ID,
		Status:         intent.ResponseCompleted,
		Usage:          usageOf(parsed.Usage),
	}, nil
}

// encode builds the request body and rejects a schema that is not valid JSON
// before anything reaches the network.
func (p *Provider) encode(request intent.ProviderRequest) ([]byte, error) {
	if !json.Valid([]byte(request.JSONSchema)) {
		return nil, intent.NewProviderError(intent.ErrorInvalidResponse, 0, "output schema is not valid JSON")
	}
	schemaName := request.SchemaName
	if schemaName == "" {
		schemaName = intent.SchemaName
	}
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = intent.DefaultBounds().MaxOutputTokens
	}
	body := requestBody{
		Model:           p.model,
		Store:           false,
		Instructions:    request.Instructions,
		Input:           request.Input,
		MaxOutputTokens: maxTokens,
		Reasoning:       reasoning{Effort: ReasoningEffort},
		Text: textOptions{Format: formatSpec{
			Type:   "json_schema",
			Name:   schemaName,
			Strict: true,
			Schema: json.RawMessage(request.JSONSchema),
		}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, intent.NewProviderError(intent.ErrorInvalidResponse, 0, "cannot encode request")
	}
	return encoded, nil
}

// extractText collects every output_text payload and reports whether the model
// refused. Reasoning payloads are never read: only the final structured output
// is consumed, so hidden reasoning is neither requested nor stored.
func extractText(parsed *envelope) (string, bool) {
	var builder strings.Builder
	refused := false
	for _, item := range parsed.Output {
		if item.Type == "refusal" || item.Refusal != "" {
			refused = true
		}
		for _, content := range item.Content {
			switch content.Type {
			case "refusal":
				refused = true
			case "output_text":
				builder.WriteString(content.Text)
			}
		}
	}
	return builder.String(), refused
}

func usageOf(usage *usageDetails) intent.Usage {
	if usage == nil {
		return intent.Usage{}
	}
	result := intent.Usage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	}
	if usage.OutputTokensDetails != nil {
		result.ReasoningTokens = usage.OutputTokensDetails.ReasoningTokens
	}
	return result
}

func incompleteReason(parsed *envelope) string {
	if parsed.IncompleteDetails == nil {
		return "provider returned an incomplete response"
	}
	return "provider returned an incomplete response (" + safeProviderText(parsed.IncompleteDetails.Reason) + ")"
}

// statusError classifies a non-2xx response. Only safe, bounded detail is
// kept: never an authorization header, never an API key and never a raw body.
func statusError(status int, body []byte) error {
	kind := classifyStatus(status)
	message := ""
	code, detail := parseProviderError(body)
	if code != "" {
		message = "provider error " + safeCode(code)
	}
	// A 400 or 422 is a request-contract problem whose structured detail is
	// safe and useful. Auth, rate-limit and server errors are never echoed:
	// their bodies may echo credential material.
	if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
		if detail != "" {
			if message != "" {
				message += ": "
			}
			message += safeProviderText(detail)
		}
	} else if message == "" {
		message = fmt.Sprintf("provider responded HTTP %d", status)
	}
	return intent.NewProviderError(kind, status, message)
}

func classifyStatus(status int) intent.ErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return intent.ErrorAuthentication
	case http.StatusTooManyRequests:
		return intent.ErrorRateLimited
	case http.StatusRequestTimeout:
		return intent.ErrorTimeout
	default:
		return intent.ErrorUnavailable
	}
}

func classifyCode(code string) intent.ErrorKind {
	switch {
	case strings.Contains(code, "rate_limit"):
		return intent.ErrorRateLimited
	case strings.Contains(code, "api_key"),
		strings.Contains(code, "authentication"),
		strings.Contains(code, "permission"),
		strings.Contains(code, "authorization"):
		return intent.ErrorAuthentication
	default:
		return intent.ErrorUnavailable
	}
}

// transportError classifies transport failures. A context deadline is a
// timeout; a cancellation is reported as a plain provider failure so it is not
// mislabelled as a budget the provider exceeded.
func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil || strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		return intent.NewProviderError(intent.ErrorTimeout, 0, "call exceeded its time budget")
	}
	return fmt.Errorf("%w: call did not complete", intent.ErrProvider)
}

// parseProviderError extracts a structured provider error without ever
// returning the raw body.
func parseProviderError(body []byte) (code, detail string) {
	var parsed struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == nil {
		return "", ""
	}
	return parsed.Error.Code, parsed.Error.Message
}

// safeCode keeps only a short provider error code.
func safeCode(code string) string { return safeProviderText(code) }

// safeStatus keeps only a short provider status word.
func safeStatus(status string) string { return safeProviderText(status) }

// safeProviderText bounds and sanitises one provider-supplied word or short
// sentence so it cannot become a log-injection or credential-leak vector.
func safeProviderText(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}
	}, value)
	value = strings.TrimSpace(value)
	if len(value) > maxProviderDetailLen {
		value = value[:maxProviderDetailLen] + "..."
	}
	return value
}
