package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/catalog"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/config"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/enrichment"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/policy"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store"
)

// Stable tool error codes. The vocabulary is deliberately small and matches
// the meanings the HTTP surface already uses, but it is declared here: MCP is
// its own transport and does not import internal/api for error types.
const (
	CodeInvalidRequest      = "invalid_request"
	CodeNotFound            = "not_found"
	CodeConflict            = "conflict"
	CodeExternalOperations  = "external_operations_disabled"
	CodeUpstreamRateLimited = "upstream_rate_limited"
	CodeUpstreamTimeout     = "upstream_timeout"
	CodeUpstreamUnavailable = "upstream_unavailable"
	CodeAllProvidersFailed  = "all_providers_failed"
	CodeInternalError       = "internal_error"
)

// errExternalOperations marks a tool gated behind the MCP external-operations
// switch.
var errExternalOperations = errors.New("mcpserver: external operations are disabled")

// CodeOrder lists every stable tool error code in a fixed order so the
// vocabulary stays small, closed and testable.
func CodeOrder() []string {
	return []string{
		CodeInvalidRequest,
		CodeNotFound,
		CodeConflict,
		CodeExternalOperations,
		CodeUpstreamRateLimited,
		CodeUpstreamTimeout,
		CodeUpstreamUnavailable,
		CodeAllProvidersFailed,
		CodeInternalError,
	}
}

// Transport-level validation failures raised by this package itself.
var (
	errInvalidPolicyDTO    = errors.New("mcpserver: invalid policy")
	errInvalidCatalog      = errors.New("mcpserver: invalid catalog request")
	errInvalidEvidencePage = errors.New("mcpserver: invalid evidence page request")
	errMissingService      = errors.New("mcpserver: service is not configured")
)

// maxIssueDetail bounds how many upstream issue messages are echoed inside an
// all-providers-failed tool error.
const maxIssueDetail = 6

// allProvidersFailed renders an upstream failure that still carries the safe
// provider messages, without leaking a token, a database URL or a raw
// provider response body.
func allProvidersFailed(messages []string) *ToolError {
	detail := "every configured provider failed operationally"
	if len(messages) > 0 {
		detail += ": " + strings.Join(messages, "; ")
	}
	return newToolError(CodeAllProvidersFailed, detail)
}

// ToolError is a safe, structured tool failure.
//
// It implements error, so the SDK reports it with MCP tool-error semantics
// (IsError true, message visible to the model) instead of as a protocol
// failure the model could never read.
type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error renders the failure as the short text a model reads first. The code is
// the token before the first colon, so callers can branch without parsing
// prose.
func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// AsToolError reports whether err is a structured tool failure.
func AsToolError(err error) (*ToolError, bool) {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr, true
	}
	return nil, false
}

// newToolError builds a tool failure with an optional formatted message.
func newToolError(code, message string, args ...any) *ToolError {
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	return &ToolError{Code: code, Message: message}
}

// externalOperationsDisabled is the shared failure for the two MCP tools that
// spend provider quota while the switch is off.
func externalOperationsDisabled() *ToolError {
	return newToolError(CodeExternalOperations,
		"discovery and enrichment are disabled for MCP; set "+config.EnvMCPEnableExternalOperations+"=true to enable them")
}

// classify maps an application failure onto the tool error vocabulary.
//
// It never returns a raw provider response, a database URL, a token or a Go
// stack trace: unknown failures collapse to a generic internal_error.
func classify(err error) *ToolError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound),
		errors.Is(err, outcome.ErrResolutionNotFound):
		return newToolError(CodeNotFound, "the requested resource does not exist")
	case errors.Is(err, errExternalOperations):
		return externalOperationsDisabled()
	case errors.Is(err, errMissingService):
		return newToolError(CodeInternalError, "this tool is not configured on this server")
	case errors.Is(err, context.DeadlineExceeded):
		return newToolError(CodeUpstreamTimeout, "the operation exceeded its time budget")
	case errors.Is(err, context.Canceled):
		return newToolError(CodeInternalError, "the operation was cancelled")
	case errors.Is(err, discovery.ErrEvidenceConflict),
		errors.Is(err, enrichment.ErrEvidenceConflict),
		errors.Is(err, catalog.ErrEvidenceConflict):
		return newToolError(CodeConflict,
			"evidence identity conflict: the id already exists with different content")
	}

	var discoveryFailed *discovery.ProvidersFailedError
	if errors.As(err, &discoveryFailed) {
		return newToolError(CodeAllProvidersFailed,
			"every configured discovery provider failed operationally; inspect the provider issues")
	}
	var enrichFailed *enrichment.ProvidersFailedError
	if errors.As(err, &enrichFailed) {
		return newToolError(CodeAllProvidersFailed,
			"every applicable enrichment provider failed operationally; inspect the provider issues")
	}
	if errors.Is(err, discovery.ErrProviderUnavailable) {
		return newToolError(CodeUpstreamUnavailable, "a configured discovery provider is unavailable")
	}

	if isRequestShapeError(err) {
		return newToolError(CodeInvalidRequest, safeDetail(err))
	}
	return newToolError(CodeInternalError, "the request could not be completed")
}

// isRequestShapeError reports whether err is one of the deterministic checks
// the application performs on the caller's own input. Those are client
// problems, not server faults.
func isRequestShapeError(err error) bool {
	switch {
	case errors.Is(err, policy.ErrInvalidPolicy),
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
		errors.Is(err, discovery.ErrProfile),
		errors.Is(err, enrichment.ErrNoSpecimens),
		errors.Is(err, enrichment.ErrTooManySpecimens),
		errors.Is(err, enrichment.ErrDuplicateSpecimen),
		errors.Is(err, errInvalidPolicyDTO),
		errors.Is(err, errInvalidCatalog),
		errors.Is(err, errInvalidEvidencePage),
		errors.Is(err, outcome.ErrInvalidKind),
		errors.Is(err, outcome.ErrInvalidResolutionID),
		errors.Is(err, outcome.ErrInvalidLimit),
		errors.Is(err, outcome.ErrNoteTooLong),
		errors.Is(err, outcome.ErrNoteNotUTF8):
		return true
	}
	return false
}

// safeDetail renders a domain validation error as client-facing detail. It is
// bounded so an unexpected error can never flood the response.
func safeDetail(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "the request is invalid"
	}
	const maxRunes = 400
	runes := []rune(msg)
	if len(runes) > maxRunes {
		msg = string(runes[:maxRunes])
	}
	return msg
}
