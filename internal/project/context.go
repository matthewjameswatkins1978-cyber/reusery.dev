package project

import (
	"fmt"
)

// BuildContext assembles the immutable snapshot a decision will record.
//
// It contains fingerprint identity and active preference effects only: no raw
// source, no local path, no evidence. Its hash is what a Resolution remembers,
// so the decision keeps resolving to its own context after preferences or the
// fingerprint later change.
func BuildContext(project Project, fingerprint Fingerprint, sha, revision string, preferences []Preference) Context {
	effects := make([]PreferenceEffect, 0, len(preferences))
	for _, preference := range preferences {
		effects = append(effects, PreferenceEffect{
			PreferenceID: preference.ID,
			Kind:         preference.Kind,
			PrimitiveID:  preference.PrimitiveID,
			CandidateID:  preference.CandidateID,
			TextValue:    preference.TextValue,
			IntValue:     preference.IntValue,
			SourceReason: preference.SourceReason,
		})
	}
	return Context{
		SchemaVersion:     SchemaVersion,
		ProjectID:         project.ID,
		Language:          fingerprint.Language,
		SourceKind:        project.SourceKind,
		SourceRevision:    revision,
		FingerprintSHA256: sha,
		Preferences:       effects,
	}
}

// ContextHashOf returns the SHA-256 that identifies a snapshot.
func ContextHashOf(context Context) (string, error) {
	hash, err := ContextHash(context)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// EffectivePolicyID composes the deterministic identity of a project-aware
// policy. It is derived, never random, so the same base policy applied to the
// same context always reports the same id.
func EffectivePolicyID(basePolicyID, contextHash string) string {
	return fmt.Sprintf("%s+project:%s", basePolicyID, contextHash)
}

// activePreferences filters out revoked memories. Forgetting sets a timestamp;
// it never deletes the row, so history survives while the effect does not.
func activePreferences(preferences []Preference) []Preference {
	out := make([]Preference, 0, len(preferences))
	for _, preference := range preferences {
		if preference.Active() {
			out = append(out, preference)
		}
	}
	return out
}
