// Package intent normalises ordinary engineering language into a bounded,
// inspectable, machine-readable behavioural contract draft.
//
// The model may structure intent. It may not establish engineering fact. This
// package never creates model.Evidence, never records provenance, never scores
// or ranks candidates and never produces a resolver outcome. The verdict is a
// status — ready, needs_clarification or unsupported — never a confidence
// score, and the generated contract is provisional until a human accepts it.
//
// Three things stay separate: the provider-independent intent domain in this
// package, the normalisation service that owns prompt, schema, validation,
// repair and deterministic identifiers, and the model adapter under
// providers/openai that speaks one provider's wire protocol.
package intent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Status is the normalisation verdict. There is deliberately no score and no
// confidence percentage anywhere in this package.
type Status string

const (
	// StatusReady means the input is specific enough to produce a useful
	// provisional behavioural contract.
	StatusReady Status = "ready"
	// StatusNeedsClarification means a material ambiguity could substantially
	// change the required behaviour, architecture, platform assumptions,
	// solution level, licence constraints, security semantics or failure
	// semantics. It is a valid product result, not a failure.
	StatusNeedsClarification Status = "needs_clarification"
	// StatusUnsupported means the input is not a meaningful engineering
	// selection, reuse or resolution request. It is also a valid result.
	StatusUnsupported Status = "unsupported"
)

// Valid reports whether the status is one Reusery understands.
func (s Status) Valid() bool {
	switch s {
	case StatusReady, StatusNeedsClarification, StatusUnsupported:
		return true
	default:
		return false
	}
}

// ArtifactLevel records what the user's request implies about the level of
// artefact they want. It is not a resolver verdict: nothing in this package
// decides reuse, adapt, depend, reference or build locally.
type ArtifactLevel string

const (
	ArtifactUnspecified ArtifactLevel = "unspecified"
	ArtifactCode        ArtifactLevel = "code"
	ArtifactPackage     ArtifactLevel = "package"
	ArtifactLibrary     ArtifactLevel = "library"
	ArtifactFramework   ArtifactLevel = "framework"
	ArtifactCrossLevel  ArtifactLevel = "cross_level"
)

// Valid reports whether the artefact level is one Reusery understands.
func (l ArtifactLevel) Valid() bool {
	switch l {
	case ArtifactUnspecified, ArtifactCode, ArtifactPackage, ArtifactLibrary,
		ArtifactFramework, ArtifactCrossLevel:
		return true
	default:
		return false
	}
}

// Requirement is one model-structured behavioural claim before Reusery assigns
// an identifier. The model has no authority over identifiers.
type Requirement struct {
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
}

// Constraint is one explicit project or user limit the request actually
// states. A stated preference stays optional.
type Constraint struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Ambiguity is one material open question. It is raised only when the answer
// could substantially change the contract or the solution search.
type Ambiguity struct {
	Question     string `json:"question"`
	WhyItMatters string `json:"why_it_matters"`
}

// Draft is the model's structured output. It contains no identifiers, no
// evidence, no candidates and no resolver outcome, and the strict JSON schema
// has no fields in which such authority could be expressed.
type Draft struct {
	Status                 Status        `json:"status"`
	Capability             string        `json:"capability"`
	Summary                string        `json:"summary"`
	RequestedArtifactLevel ArtifactLevel `json:"requested_artifact_level"`
	Requirements           []Requirement `json:"requirements"`
	Constraints            []Constraint  `json:"constraints"`
	Ambiguities            []Ambiguity   `json:"ambiguities"`
	Assumptions            []string      `json:"assumptions"`
	UnsupportedReason      string        `json:"unsupported_reason"`
}

// Usage is raw token accounting. No monetary cost is calculated: API pricing
// changes, raw token usage is durable evidence.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// Add returns the component-wise sum of two usages. Repair accounting totals
// both paid calls.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:     u.InputTokens + other.InputTokens,
		OutputTokens:    u.OutputTokens + other.OutputTokens,
		ReasoningTokens: u.ReasoningTokens + other.ReasoningTokens,
		TotalTokens:     u.TotalTokens + other.TotalTokens,
	}
}

// GenerationMetadata is provenance for the normalisation event. It is not
// engineering evidence and must never be converted into model.Evidence.
type GenerationMetadata struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ResponseID    string `json:"response_id"`
	PromptVersion string `json:"prompt_version"`
	SchemaVersion int    `json:"schema_version"`
	Calls         int    `json:"calls"`
	Repaired      bool   `json:"repaired"`
	Usage         Usage  `json:"usage"`
}

// Result is the canonical normalisation output.
//
// Capability and Summary are always present so an unsupported or
// clarification result stays inspectable even when no contract could be
// generated. Primitive and Contract are generated only when the draft carries
// requirements; they are omitted otherwise. They are provisional: a generated
// contract is not canonical engineering truth, is never persisted in this
// packet, and must not be resolved automatically.
type Result struct {
	Input                  string             `json:"input"`
	Status                 Status             `json:"status"`
	RequestedArtifactLevel ArtifactLevel      `json:"requested_artifact_level"`
	Capability             string             `json:"capability"`
	Summary                string             `json:"summary"`
	Primitive              *model.Primitive   `json:"primitive,omitempty"`
	Contract               *model.Contract    `json:"contract,omitempty"`
	Constraints            []Constraint       `json:"constraints"`
	Ambiguities            []Ambiguity        `json:"ambiguities"`
	Assumptions            []string           `json:"assumptions"`
	UnsupportedReason      string             `json:"unsupported_reason,omitempty"`
	Metadata               GenerationMetadata `json:"metadata"`
}

// Bounds are the fixed conservative Packet 6 limits. They are deliberately a
// constant rather than a configuration surface.
type Bounds struct {
	MaxInputBytes                  int
	MaxRequirements                int
	MaxConstraints                 int
	MaxAmbiguities                 int
	MaxAssumptions                 int
	MaxRequirementDescriptionBytes int
	MaxConstraintDescriptionBytes  int
	MaxAmbiguityQuestionBytes      int
	MaxAmbiguityWhyBytes           int
	MaxAssumptionBytes             int
	MaxSummaryBytes                int
	MaxCapabilityBytes             int
	MaxUnsupportedReasonBytes      int
	MaxCalls                       int
	MaxOutputTokens                int
	Timeout                        time.Duration
}

// DefaultBounds returns the Packet 6 limits. Two model calls per
// normalisation is an absolute maximum: one initial call and at most one
// bounded repair.
func DefaultBounds() Bounds {
	return Bounds{
		MaxInputBytes:                  8192,
		MaxRequirements:                24,
		MaxConstraints:                 20,
		MaxAmbiguities:                 12,
		MaxAssumptions:                 12,
		MaxRequirementDescriptionBytes: 400,
		MaxConstraintDescriptionBytes:  400,
		MaxAmbiguityQuestionBytes:      400,
		MaxAmbiguityWhyBytes:           400,
		MaxAssumptionBytes:             400,
		MaxSummaryBytes:                600,
		MaxCapabilityBytes:             160,
		MaxUnsupportedReasonBytes:      600,
		MaxCalls:                       2,
		MaxOutputTokens:                2500,
		Timeout:                        20 * time.Second,
	}
}

// Sentinel errors. Their messages are deterministic and carry no provider
// response body, no API key and no implementation internals.
var (
	// ErrInvalidInput marks a local input problem that consumed zero model
	// calls: empty, oversized or not valid UTF-8.
	ErrInvalidInput = errors.New("intent: invalid input")
	// ErrValidation marks a draft that still failed deterministic semantic
	// validation after the single permitted repair call.
	ErrValidation = errors.New("intent: normalised draft failed semantic validation")
	// ErrDecode marks structured model output that is not decodable.
	ErrDecode = errors.New("intent: cannot decode structured model output")
	// ErrProvider wraps every model-provider failure.
	ErrProvider = errors.New("intent: model provider failure")
)

// ErrorKind classifies a provider failure. Kinds are inspectable and safe:
// they never carry an authorization header, an API key or a response body.
type ErrorKind string

const (
	ErrorAuthentication  ErrorKind = "authentication"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorRefused         ErrorKind = "refused"
	ErrorIncomplete      ErrorKind = "incomplete"
	ErrorInvalidResponse ErrorKind = "invalid_response"
)

// Valid reports whether the kind is one Reusery classifies.
func (k ErrorKind) Valid() bool {
	switch k {
	case ErrorAuthentication, ErrorRateLimited, ErrorTimeout, ErrorUnavailable,
		ErrorRefused, ErrorIncomplete, ErrorInvalidResponse:
		return true
	default:
		return false
	}
}

// ProviderError is an inspectable, bounded provider failure. Message carries
// only short, safe provider detail: a status code, a provider error code or an
// incompleteness reason. Never a header, never a key, never a raw body.
type ProviderError struct {
	Kind       ErrorKind
	Message    string
	StatusCode int
}

func (e *ProviderError) Error() string {
	message := fmt.Sprintf("intent: provider %s", e.Kind)
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (status %d)", e.StatusCode)
	}
	if e.Message != "" {
		message += ": " + e.Message
	}
	return message
}

// Unwrap puts every provider failure behind ErrProvider so callers can
// separate provider failure from input and validation failure.
func (e *ProviderError) Unwrap() error { return ErrProvider }

// NewProviderError builds a classified provider failure.
func NewProviderError(kind ErrorKind, statusCode int, message string) *ProviderError {
	return &ProviderError{Kind: kind, StatusCode: statusCode, Message: message}
}

// ProviderRequest carries only what a model adapter genuinely needs. No
// Reusery domain types, no database types and no HTTP request objects appear
// here.
type ProviderRequest struct {
	// Instructions is the code-owned prompt, already rendered.
	Instructions string
	// Input is the user's engineering request, kept separate from
	// Instructions so the boundary between instructions and data stays clear.
	Input string
	// JSONSchema is the strict output schema for structured output.
	JSONSchema string
	// SchemaName names the structured output schema.
	SchemaName string
	// MaxOutputTokens bounds the generated completion.
	MaxOutputTokens int
	// Repair is set only on the single bounded repair call. Instructions
	// already contains the rendered repair text; Repair keeps the structured
	// form so adapters and tests can tell a repair from an initial call.
	Repair *RepairContext
}

// RepairContext is the structured form of a bounded semantic repair.
type RepairContext struct {
	PriorDraft string
	Failures   []string
}

// ResponseStatus records the provider-neutral status of a usable structured
// response. Non-usable outcomes are returned as *ProviderError instead.
type ResponseStatus string

// ResponseCompleted is the only status a usable response carries.
const ResponseCompleted ResponseStatus = "completed"

// ProviderResponse is a provider's bounded output. StructuredJSON is the
// model's structured draft as JSON text; Reusery never consumes anything else
// the model produced.
type ProviderResponse struct {
	StructuredJSON string
	ProviderID     string
	Model          string
	ResponseID     string
	Status         ResponseStatus
	Usage          Usage
}

// Provider is the replaceable model-provider boundary. The normalisation
// service depends only on this interface, so the OpenAI adapter is one
// implementation among possible others.
//
// A provider returns a non-nil error when it produced no usable structured
// output at all. It never returns raw reasoning, never returns a partial
// object alongside an error, and never places an API key in an error.
type Provider interface {
	ID() string
	Generate(context.Context, ProviderRequest) (ProviderResponse, error)
}

// Normalizer turns raw engineering intent into a bounded Result. It owns the
// prompt version, the schema version, deterministic validation, the single
// repair policy and deterministic identifier generation. It depends on a
// Provider and on nothing else: no database, no discovery and no resolver.
type Normalizer struct {
	provider Provider
	bounds   Bounds
}

// NewNormalizer builds a normaliser with the fixed Packet 6 bounds.
func NewNormalizer(provider Provider) *Normalizer {
	return newNormalizer(provider, DefaultBounds())
}

func newNormalizer(provider Provider, bounds Bounds) *Normalizer {
	return &Normalizer{provider: provider, bounds: bounds}
}
