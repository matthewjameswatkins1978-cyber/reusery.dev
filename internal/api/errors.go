package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// Stable machine-readable error codes. The vocabulary is deliberately small:
// one code describes a class of failure, not an individual Go sentinel.
const (
	CodeBadRequest                = "bad_request"
	CodeValidationFailed          = "validation_failed"
	CodeInvalidRequest            = "invalid_request"
	CodeNotFound                  = "not_found"
	CodeConflict                  = "conflict"
	CodeExternalOperations        = "external_operations_disabled"
	CodeModelProviderUnconfigured = "model_provider_unconfigured"
	CodeUpstreamAuthentication    = "upstream_authentication"
	CodeUpstreamRateLimited       = "upstream_rate_limited"
	CodeUpstreamTimeout           = "upstream_timeout"
	CodeUpstreamUnavailable       = "upstream_unavailable"
	CodeAllProvidersFailed        = "all_providers_failed"
	CodeInternalError             = "internal_error"
)

// Problem is the RFC 9457 problem+json body returned for every API error.
//
// It adds one stable machine-readable extension, code, so clients can branch
// on a small documented vocabulary instead of parsing human detail text.
type Problem struct {
	// Type is a URI reference for the problem type. Reusery does not publish
	// per-error documentation pages yet, so it is the RFC 9457 default.
	Type string `json:"type,omitempty" format:"uri" example:"about:blank" doc:"URI reference for the problem type."`
	// Title is the HTTP status text and never changes for a given status.
	Title string `json:"title" example:"Unprocessable Entity" doc:"Short, human-readable summary of the problem type."`
	// Status repeats the HTTP status code for client convenience.
	Status int `json:"status" example:"422" doc:"HTTP status code."`
	// Detail explains this specific occurrence.
	Detail string `json:"detail" example:"candidate request is invalid" doc:"Human-readable explanation specific to this occurrence."`
	// Code is the stable machine-readable error code.
	Code string `json:"code" example:"invalid_request" doc:"Stable machine-readable error code from the documented API vocabulary."`
	// Errors carries per-field validation detail where Huma produced it.
	Errors []*huma.ErrorDetail `json:"errors,omitempty" doc:"Optional list of individual error details."`
}

// Error implements error.
func (p *Problem) Error() string { return p.Detail }

// GetStatus implements huma.StatusError.
func (p *Problem) GetStatus() int { return p.Status }

// ContentType makes every error response application/problem+json.
func (p *Problem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// problem builds a problem+json body with the given status, code and detail.
func problem(status int, code, detail string, errs ...error) *Problem {
	details := make([]*huma.ErrorDetail, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		var detailer huma.ErrorDetailer
		if errors.As(err, &detailer) {
			details = append(details, detailer.ErrorDetail())
			continue
		}
		details = append(details, &huma.ErrorDetail{Message: err.Error()})
	}
	p := &Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	}
	if len(details) > 0 {
		p.Errors = details
	}
	return p
}

// defaultCodeFor assigns a code to errors Huma raises on its own behalf, such
// as body-limit and schema-validation failures.
func defaultCodeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType:
		return CodeInvalidRequest
	case http.StatusUnprocessableEntity:
		return CodeValidationFailed
	case http.StatusBadGateway:
		return CodeAllProvidersFailed
	case http.StatusServiceUnavailable:
		return CodeUpstreamUnavailable
	case http.StatusGatewayTimeout:
		return CodeUpstreamTimeout
	case http.StatusInternalServerError:
		return CodeInternalError
	}
	if status >= 400 && status < 500 {
		return CodeBadRequest
	}
	if status >= 500 {
		return CodeInternalError
	}
	return ""
}

// installProblemHook replaces Huma's global error constructor once, so every
// error Huma raises on its own behalf carries the stable code extension and
// the RFC 9457 content type. It runs before any operation is registered so
// the generated error schema includes the code field.
func installProblemHook() {
	problemHookOnce.Do(func() {
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			return problem(status, defaultCodeFor(status), msg, errs...)
		}
	})
}

// classify maps an application or provider failure onto the API's error
// contract. It never returns a raw provider message, a database URL, a token
// or a provider response body.
func classify(err error) *Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return problem(http.StatusNotFound, CodeNotFound, "the requested resource does not exist")
	case errors.Is(err, errExternalOperations):
		return problem(http.StatusServiceUnavailable, CodeExternalOperations,
			"HTTP model, discovery and enrichment operations are disabled; set "+
				config.EnvAPIEnableExternalOperations+"=true to enable them")
	case errors.Is(err, config.ErrMissingOpenAIAPIKey):
		return problem(http.StatusServiceUnavailable, CodeModelProviderUnconfigured,
			"the model provider is not configured; set REUSERY_OPENAI_API_KEY")
	case errors.Is(err, discovery.ErrEvidenceConflict),
		errors.Is(err, enrichment.ErrEvidenceConflict),
		errors.Is(err, catalog.ErrEvidenceConflict):
		return problem(http.StatusConflict, CodeConflict, "evidence identity conflict: the id already exists with different content")
	case errors.Is(err, context.DeadlineExceeded):
		return problem(http.StatusGatewayTimeout, CodeUpstreamTimeout, "the operation exceeded its time budget")
	case errors.Is(err, context.Canceled):
		return problem(http.StatusInternalServerError, CodeInternalError, "the operation was cancelled")
	}

	var failed *discovery.ProvidersFailedError
	if errors.As(err, &failed) {
		return problem(http.StatusBadGateway, CodeAllProvidersFailed,
			"every configured provider failed operationally; inspect the provider issues for detail")
	}
	var enrichFailed *enrichment.ProvidersFailedError
	if errors.As(err, &enrichFailed) {
		return problem(http.StatusBadGateway, CodeAllProvidersFailed,
			"every applicable provider failed operationally; inspect the provider issues for detail")
	}

	var providerErr *intent.ProviderError
	if errors.As(err, &providerErr) {
		return providerProblem(providerErr)
	}

	if isDomainValidation(err) {
		return problem(http.StatusUnprocessableEntity, CodeInvalidRequest, safeDetail(err))
	}
	return problem(http.StatusInternalServerError, CodeInternalError, "the request could not be completed")
}

// providerProblem maps a model-provider failure onto its documented status.
// The provider message is reduced to a short, non-leaking explanation.
func providerProblem(err *intent.ProviderError) *Problem {
	detail := "the model provider could not complete the request"
	switch err.Kind {
	case intent.ErrorAuthentication:
		return problem(http.StatusServiceUnavailable, CodeUpstreamAuthentication, detail)
	case intent.ErrorRateLimited:
		return problem(http.StatusServiceUnavailable, CodeUpstreamRateLimited, detail)
	case intent.ErrorTimeout:
		return problem(http.StatusGatewayTimeout, CodeUpstreamTimeout, detail)
	case intent.ErrorUnavailable, intent.ErrorRefused, intent.ErrorIncomplete, intent.ErrorInvalidResponse:
		return problem(http.StatusBadGateway, CodeUpstreamUnavailable, detail)
	}
	return problem(http.StatusBadGateway, CodeUpstreamUnavailable, detail)
}

// isDomainValidation reports whether err is one of the deterministic request
// checks the application performs after transport validation. Those are client
// problems, not server faults.
func isDomainValidation(err error) bool {
	switch {
	case errors.Is(err, intent.ErrInvalidInput),
		errors.Is(err, intent.ErrValidation),
		errors.Is(err, discovery.ErrProfile),
		errors.Is(err, enrichment.ErrNoSpecimens),
		errors.Is(err, enrichment.ErrTooManySpecimens),
		errors.Is(err, enrichment.ErrDuplicateSpecimen),
		errors.Is(err, policy.ErrInvalidPolicy),
		errors.Is(err, policy.ErrUnsupportedSchema),
		errors.Is(err, policy.ErrPathEscape),
		errors.Is(err, policy.ErrInvalidFeedback),
		errors.Is(err, policy.ErrUnknownFeedbackCandidate),
		errors.Is(err, policy.ErrUnsupportedFeedback),
		errors.Is(err, policy.ErrDependencyCountUnknown),
		errors.Is(err, policy.ErrDependencyCountZero),
		errors.Is(err, policy.ErrNotArchived),
		errors.Is(err, resolver.ErrUnsupportedReuseMode),
		errors.Is(err, resolver.ErrReuseModeNotDeclared),
		errors.Is(err, resolver.ErrDuplicateCandidateID),
		errors.Is(err, resolver.ErrEmptyPrimitiveID),
		errors.Is(err, resolver.ErrPrimitiveContractMismatch),
		errors.Is(err, resolver.ErrContractPrimitiveMismatch),
		errors.Is(err, resolver.ErrZeroResolvedAt),
		errors.Is(err, resolver.ErrCandidatePrimitiveMismatch),
		errors.Is(err, errInvalidCursor),
		errors.Is(err, errInvalidPagination):
		return true
	}
	return false
}

// safeDetail renders a domain validation error as client-facing detail.
// It is truncated so an unexpected error can never flood the response.
func safeDetail(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "the request is invalid"
	}
	const maxDetail = 400
	if len(msg) > maxDetail {
		msg = msg[:maxDetail]
	}
	return msg
}

// errExternalOperations marks an operation that is gated behind the
// external-operations switch.
var errExternalOperations = errors.New("api: external operations are disabled")
