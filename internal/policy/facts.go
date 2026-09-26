package policy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
)

// Evidence kinds the fact extractor understands. Discovery (Packet 5) and
// enrichment (Packet 7) both use these names; only evidence carrying a versioned
// fact artifact supplies a machine value.
const (
	KindSourceLicense           = "source_license"
	KindLicenceRelationship     = "licence_relationship"
	KindKnownAdvisory           = "known_advisory"
	KindKnownAdvisoryCount      = "known_advisory_count"
	KindDirectDependency        = "direct_dependency"
	KindDirectDependencyCount   = "direct_dependency_count"
	KindIndirectDependency      = "indirect_dependency"
	KindIndirectDependencyCount = "indirect_dependency_count"
	KindRepositoryArchived      = "repository_archived"
	KindRepositoryPushedAt      = "repository_pushed_at"
	KindPackagePublishedAt      = "package_published_at"
	KindPackageDeprecated       = "package_deprecated"
	KindPackageDeprecatedReason = "package_deprecated_reason"
	KindSourceRevision          = "source_revision"
	KindPackageVersion          = "package_version"
	KindDiscoveryMatch          = "discovery_match"
)

// FactArtifactSchemaVersion is the only fact artifact schema supported.
const FactArtifactSchemaVersion = 1

// FactStatus describes what stored evidence establishes about one fact.
//
// "multiple" is licence-specific: a provider that returned several expressions
// while admitting it cannot say how they relate is different from two
// providers disagreeing, and policy treats them differently.
type FactStatus string

const (
	FactKnown       FactStatus = "known"
	FactUnknown     FactStatus = "unknown"
	FactMultiple    FactStatus = "multiple"
	FactConflicting FactStatus = "conflicting"
)

// Fact artifact envelope. Policy never parses Evidence.Claim: the artifact is
// the only machine input.
type factArtifact struct {
	SchemaVersion int             `json:"schema_version"`
	Value         json.RawMessage `json:"value"`
}

// EncodeFactArtifact renders one machine-readable fact value as canonical JSON.
// Field order is fixed by the struct, so the same value always renders
// byte-identically and feeds the deterministic evidence identity digest.
func EncodeFactArtifact(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("policy: encode fact value: %w", err)
	}
	payload, err := json.Marshal(factArtifact{SchemaVersion: FactArtifactSchemaVersion, Value: encoded})
	if err != nil {
		return "", fmt.Errorf("policy: encode fact artifact: %w", err)
	}
	return string(payload), nil
}

// factValue decodes the value of a fact artifact into the expected Go type. A
// missing, malformed, unknown-schema or wrong-typed artifact reports ok=false:
// a malformed artifact becomes an unknown fact, never a guessed one.
func factValue[T any](raw string) (value T, ok bool) {
	var zero T
	if raw == "" {
		return zero, false
	}
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return zero, false
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var artifact factArtifact
	if err := decoder.Decode(&artifact); err != nil {
		return zero, false
	}
	if artifact.SchemaVersion != FactArtifactSchemaVersion {
		return zero, false
	}
	var decoded T
	if err := json.Unmarshal(artifact.Value, &decoded); err != nil {
		return zero, false
	}
	return decoded, true
}

// hasMalformedFactArtifact reports whether an artifact claims to be a fact
// envelope but cannot be read as one.
func hasMalformedFactArtifact(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	_, ok := factValue[json.RawMessage](raw)
	return !ok
}

// LicenceFact is the exact licence evidence recorded for one candidate.
type LicenceFact struct {
	Status              FactStatus `json:"status"`
	Values              []string   `json:"values,omitempty"`
	RelationshipUnknown bool       `json:"relationship_unknown,omitempty"`
	EvidenceIDs         []string   `json:"evidence_ids,omitempty"`
}

// AdvisoryFact records advisory identifiers reported for the exact version,
// plus the reported count. A reported zero is a fact, never a pass.
type AdvisoryFact struct {
	Status      FactStatus `json:"status"`
	KnownIDs    []string   `json:"known_ids,omitempty"`
	Count       int        `json:"count"`
	CountKnown  bool       `json:"count_known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// DependencyFact records declared dependency burden. Counts are conservative:
// when observations disagree the larger count is used so a maximum cannot be
// cleared by disagreement.
type DependencyFact struct {
	Status             FactStatus `json:"status"`
	Direct             []string   `json:"direct,omitempty"`
	Indirect           []string   `json:"indirect,omitempty"`
	DirectCount        int        `json:"direct_count"`
	DirectCountKnown   bool       `json:"direct_count_known"`
	IndirectCount      int        `json:"indirect_count"`
	IndirectCountKnown bool       `json:"indirect_count_known"`
	EvidenceIDs        []string   `json:"evidence_ids,omitempty"`
}

// ArchivedFact is an attributable repository archive state.
type ArchivedFact struct {
	Status      FactStatus `json:"status"`
	Archived    bool       `json:"archived"`
	Known       bool       `json:"known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// LastPushFact is the attributable last-push observation.
type LastPushFact struct {
	Status      FactStatus `json:"status"`
	PushedAt    time.Time  `json:"pushed_at,omitempty"`
	Known       bool       `json:"known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// DeprecatedFact is an attributable package deprecation observation.
type DeprecatedFact struct {
	Status      FactStatus `json:"status"`
	Deprecated  bool       `json:"deprecated"`
	Reason      string     `json:"reason,omitempty"`
	Known       bool       `json:"known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// RevisionFact is the pinned or versioned source identity. A versioned module
// version and an immutable blob SHA both count as revisions; an unpinned
// repository reference does not.
type RevisionFact struct {
	Status      FactStatus `json:"status"`
	Revision    string     `json:"revision,omitempty"`
	Known       bool       `json:"known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// PublishedAtFact is the most recent attributable package publication
// timestamp. Packet 7 has no release observation, so this is the only
// release-like fact available and max_days_since_release is evaluated against
// it.
type PublishedAtFact struct {
	Status      FactStatus `json:"status"`
	PublishedAt time.Time  `json:"published_at,omitempty"`
	Known       bool       `json:"known"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// DiscoveryRelevanceFact records whether any provider actually surfaced this
// candidate. Reference requires documented relevance, so this fact matters.
type DiscoveryRelevanceFact struct {
	Status      FactStatus `json:"status"`
	Matched     bool       `json:"matched"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

// Facts is the complete typed fact set for one candidate.
type Facts struct {
	Licence            LicenceFact            `json:"licence"`
	Advisory           AdvisoryFact           `json:"advisory"`
	Dependency         DependencyFact         `json:"dependency"`
	Archived           ArchivedFact           `json:"archived"`
	LastPush           LastPushFact           `json:"last_push"`
	Deprecated         DeprecatedFact         `json:"deprecated"`
	Revision           RevisionFact           `json:"revision"`
	PublishedAt        PublishedAtFact        `json:"published_at"`
	DiscoveryRelevance DiscoveryRelevanceFact `json:"discovery_relevance"`
}

// ExtractFacts deterministically aggregates stored evidence into typed facts.
//
// Only evidence about the given specimen is considered. Machine values come
// from versioned fact artifacts; licence and revision additionally accept the
// specimen's own source fields when discovery recorded matching INFO evidence,
// because those are structured fields rather than English claims. Evidence
// carrying a fact-shaped artifact that cannot be read marks the fact unknown
// rather than guessing.
func ExtractFacts(specimen model.Specimen, evidence []model.Evidence) Facts {
	relevant := make([]model.Evidence, 0, len(evidence))
	for _, item := range evidence {
		if item.SubjectID == specimen.ID {
			relevant = append(relevant, item)
		}
	}

	return Facts{
		Licence:            extractLicence(specimen, relevant),
		Advisory:           extractAdvisory(relevant),
		Dependency:         extractDependency(relevant),
		Archived:           extractArchived(relevant),
		LastPush:           extractLastPush(relevant),
		Deprecated:         extractDeprecated(relevant),
		Revision:           extractRevision(specimen, relevant),
		PublishedAt:        extractPublishedAt(relevant),
		DiscoveryRelevance: extractDiscoveryRelevance(relevant),
	}
}

func extractLicence(specimen model.Specimen, evidence []model.Evidence) LicenceFact {
	fact := LicenceFact{Status: FactUnknown, Values: []string{}}
	values := newOrderedSet()
	malformed := false

	for _, item := range evidence {
		if item.Kind != KindSourceLicense {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		if item.Result == model.EvidenceUnknown {
			continue
		}
		if item.Result != model.EvidenceInfo {
			continue
		}
		if value, ok := factValue[string](item.Artifact); ok {
			if value != "" {
				values.add(value)
			}
			continue
		}
		if hasMalformedFactArtifact(item.Artifact) {
			malformed = true
			continue
		}
		// Discovery records an unambiguous licence on the specimen itself.
		if specimen.Source.License != "" {
			values.add(specimen.Source.License)
		}
	}
	for _, item := range evidence {
		if item.Kind == KindLicenceRelationship && item.Result == model.EvidenceUnknown {
			fact.RelationshipUnknown = true
			fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		}
	}

	fact.Values = values.values()
	switch {
	case malformed || len(fact.Values) == 0:
		fact.Status = FactUnknown
		fact.Values = nil
	case len(fact.Values) > 1:
		if fact.RelationshipUnknown {
			fact.Status = FactMultiple
		} else {
			fact.Status = FactConflicting
		}
	default:
		fact.Status = FactKnown
	}
	return fact
}

func extractAdvisory(evidence []model.Evidence) AdvisoryFact {
	fact := AdvisoryFact{Status: FactUnknown, KnownIDs: []string{}}
	ids := newOrderedSet()
	counts := newIntSet()
	malformed := false

	for _, item := range evidence {
		if item.Kind != KindKnownAdvisory && item.Kind != KindKnownAdvisoryCount {
			continue
		}
		if item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		if item.Kind == KindKnownAdvisory {
			if value, ok := factValue[string](item.Artifact); ok && value != "" {
				ids.add(value)
				continue
			}
		} else {
			if value, ok := factValue[int](item.Artifact); ok && value >= 0 {
				counts.add(value)
				continue
			}
		}
		if hasMalformedFactArtifact(item.Artifact) {
			malformed = true
		}
	}

	if malformed {
		fact.Status = FactUnknown
		return fact
	}

	// Advisories accumulate rather than contradict: the union of observed
	// identifiers and the largest reported count are the conservative reading,
	// so a stale zero cannot erase a later observation.
	fact.KnownIDs = ids.values()
	fact.Count = len(fact.KnownIDs)
	fact.CountKnown = len(counts.values()) > 0
	for _, count := range counts.values() {
		if count > fact.Count {
			fact.Count = count
		}
	}
	if fact.CountKnown || len(fact.KnownIDs) > 0 {
		fact.Status = FactKnown
	}
	return fact
}

func extractDependency(evidence []model.Evidence) DependencyFact {
	fact := DependencyFact{Status: FactUnknown, Direct: []string{}, Indirect: []string{}}
	direct := newOrderedSet()
	indirect := newOrderedSet()
	directCounts := newIntSet()
	indirectCounts := newIntSet()
	malformed := false

	for _, item := range evidence {
		switch item.Kind {
		case KindDirectDependency, KindIndirectDependency,
			KindDirectDependencyCount, KindIndirectDependencyCount:
		default:
			continue
		}
		if item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		switch item.Kind {
		case KindDirectDependency, KindIndirectDependency:
			value, ok := factValue[string](item.Artifact)
			if !ok {
				if hasMalformedFactArtifact(item.Artifact) {
					malformed = true
				}
				continue
			}
			if item.Kind == KindDirectDependency {
				direct.add(value)
			} else {
				indirect.add(value)
			}
		default:
			value, ok := factValue[int](item.Artifact)
			if !ok || value < 0 {
				if hasMalformedFactArtifact(item.Artifact) {
					malformed = true
				}
				continue
			}
			if item.Kind == KindDirectDependencyCount {
				directCounts.add(value)
			} else {
				indirectCounts.add(value)
			}
		}
	}

	if malformed {
		fact.Status = FactUnknown
		return fact
	}

	fact.Direct = direct.values()
	fact.Indirect = indirect.values()
	// Counts are conservative: when observations disagree the larger count is
	// used, so a configured maximum cannot be cleared by disagreement.
	fact.DirectCount = maxInt(directCounts.maximum(), len(fact.Direct))
	fact.DirectCountKnown = directCounts.len() > 0 || len(fact.Direct) > 0
	fact.IndirectCount = maxInt(indirectCounts.maximum(), len(fact.Indirect))
	fact.IndirectCountKnown = indirectCounts.len() > 0 || len(fact.Indirect) > 0
	if fact.DirectCountKnown || fact.IndirectCountKnown {
		fact.Status = FactKnown
	}
	return fact
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func extractArchived(evidence []model.Evidence) ArchivedFact {
	fact := ArchivedFact{Status: FactUnknown}
	seen := newOrderedSet()

	for _, item := range evidence {
		if item.Kind != KindRepositoryArchived || item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		value, ok := factValue[bool](item.Artifact)
		if !ok {
			continue
		}
		seen.add(fmt.Sprintf("%t", value))
	}

	switch len(seen.values()) {
	case 0:
		fact.Status = FactUnknown
	case 1:
		fact.Status = FactKnown
		fact.Known = true
		fact.Archived = seen.values()[0] == "true"
	default:
		// Disagreement is preserved, not resolved: the risk-bearing state is
		// reported so policy cannot pass a candidate on conflicting evidence.
		fact.Status = FactConflicting
		fact.Known = false
		fact.Archived = true
	}
	return fact
}

func extractLastPush(evidence []model.Evidence) LastPushFact {
	fact := LastPushFact{Status: FactUnknown}
	times := make([]time.Time, 0, 2)

	for _, item := range evidence {
		if item.Kind != KindRepositoryPushedAt || item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		if value, ok := factValue[string](item.Artifact); ok {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				times = append(times, parsed.UTC())
			}
		}
	}

	switch len(times) {
	case 0:
		fact.Status = FactUnknown
	case 1:
		fact.Status = FactKnown
		fact.Known = true
		fact.PushedAt = times[0]
	default:
		fact.Status = FactConflicting
		fact.Known = true
		// Freshness thresholds use the oldest observation so disagreement can
		// never make a stale repository look recent. This is deliberately not
		// "latest wins".
		fact.PushedAt = times[0]
		for _, candidate := range times[1:] {
			if candidate.Before(fact.PushedAt) {
				fact.PushedAt = candidate
			}
		}
	}
	return fact
}

func extractDeprecated(evidence []model.Evidence) DeprecatedFact {
	fact := DeprecatedFact{Status: FactUnknown}
	seen := newOrderedSet()

	for _, item := range evidence {
		switch item.Kind {
		case KindPackageDeprecated:
			if item.Result != model.EvidenceInfo {
				continue
			}
			fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
			if value, ok := factValue[bool](item.Artifact); ok {
				seen.add(fmt.Sprintf("%t", value))
			}
		case KindPackageDeprecatedReason:
			if item.Result != model.EvidenceInfo {
				continue
			}
			fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
			if value, ok := factValue[string](item.Artifact); ok && value != "" {
				fact.Reason = value
			}
		}
	}

	switch len(seen.values()) {
	case 0:
		fact.Status = FactUnknown
	case 1:
		fact.Status = FactKnown
		fact.Known = true
		fact.Deprecated = seen.values()[0] == "true"
	default:
		fact.Status = FactConflicting
		fact.Known = false
		fact.Deprecated = true
	}
	return fact
}

var revisionKinds = map[string]bool{
	KindSourceRevision: true,
	KindPackageVersion: true,
}

func extractRevision(specimen model.Specimen, evidence []model.Evidence) RevisionFact {
	fact := RevisionFact{Status: FactUnknown}
	values := newOrderedSet()

	if specimen.Source.Revision != "" {
		values.add(specimen.Source.Revision)
	}
	for _, item := range evidence {
		if !revisionKinds[item.Kind] || item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		if item.Source.Revision != "" {
			values.add(item.Source.Revision)
		}
	}

	switch len(values.values()) {
	case 0:
		fact.Status = FactUnknown
	case 1:
		fact.Status = FactKnown
		fact.Known = true
		fact.Revision = values.values()[0]
	default:
		fact.Status = FactConflicting
	}
	return fact
}

func extractPublishedAt(evidence []model.Evidence) PublishedAtFact {
	fact := PublishedAtFact{Status: FactUnknown}
	times := make([]time.Time, 0, 2)

	for _, item := range evidence {
		if item.Kind != KindPackagePublishedAt || item.Result != model.EvidenceInfo {
			continue
		}
		fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		if value, ok := factValue[string](item.Artifact); ok {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				times = append(times, parsed.UTC())
			}
		}
	}

	switch len(times) {
	case 0:
		fact.Status = FactUnknown
	case 1:
		fact.Status = FactKnown
		fact.Known = true
		fact.PublishedAt = times[0]
	default:
		fact.Status = FactConflicting
		fact.Known = true
		// Newest publication is the release-like signal; the newest known
		// publication is what a "days since release" threshold asks for.
		fact.PublishedAt = times[0]
		for _, candidate := range times[1:] {
			if candidate.After(fact.PublishedAt) {
				fact.PublishedAt = candidate
			}
		}
	}
	return fact
}

func extractDiscoveryRelevance(evidence []model.Evidence) DiscoveryRelevanceFact {
	fact := DiscoveryRelevanceFact{Status: FactUnknown}
	for _, item := range evidence {
		if item.Kind == KindDiscoveryMatch && item.Result == model.EvidenceInfo {
			fact.Matched = true
			fact.EvidenceIDs = append(fact.EvidenceIDs, item.ID)
		}
	}
	if fact.Matched {
		fact.Status = FactKnown
	}
	return fact
}

// orderedSet is an order-preserving de-duplicator for deterministic output.
type orderedSet struct {
	order []string
	seen  map[string]struct{}
}

func newOrderedSet() *orderedSet {
	return &orderedSet{order: []string{}, seen: map[string]struct{}{}}
}

func (s *orderedSet) add(value string) {
	if _, exists := s.seen[value]; exists {
		return
	}
	s.seen[value] = struct{}{}
	s.order = append(s.order, value)
}

func (s *orderedSet) values() []string {
	return s.order
}

// intSet is the numeric counterpart: order-preserving and de-duplicating.
type intSet struct {
	order []int
	seen  map[int]struct{}
}

func newIntSet() *intSet {
	return &intSet{order: []int{}, seen: map[int]struct{}{}}
}

func (s *intSet) add(value int) {
	if _, exists := s.seen[value]; exists {
		return
	}
	s.seen[value] = struct{}{}
	s.order = append(s.order, value)
}

func (s *intSet) values() []int { return s.order }

func (s *intSet) len() int { return len(s.order) }

func (s *intSet) maximum() int {
	largest := -1
	for _, value := range s.order {
		if value > largest {
			largest = value
		}
	}
	if largest < 0 {
		return 0
	}
	return largest
}
