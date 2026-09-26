package policy

import "github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"

// PublicGoBaseline returns the built-in public-go-baseline/v1 profile.
//
// The authored YAML at policies/public-go-baseline-v1.yaml stays the
// human-readable source of record. This constructor exists so an installed
// `reusery` binary can resolve a decision outside a repository checkout, where
// that file is not present and where loading a policy from the process working
// directory would be both unreliable and a path-handling hazard.
//
// The two definitions must never drift: TestPublicGoBaselineMatchesAuthoredYAML
// deep-equals this value against the loaded YAML profile. Adding a rule to the
// YAML without adding it here fails that test, and vice versa.
func PublicGoBaseline() Policy {
	return Policy{
		SchemaVersion: SchemaVersion,
		ID:            "public-go-baseline/v1",
		Reuse: ReusePolicy{
			Allowed: []model.ReuseMode{
				model.ReuseCopy,
				model.ReuseDependency,
				model.ReuseAdapt,
				model.ReuseReference,
			},
			Preferred: nil,
		},
		Licence: LicencePolicy{
			Allow:    nil,
			Deny:     nil,
			Unknown:  ActionReview,
			Multiple: ActionReview,
			Unlisted: ActionAllow,
		},
		Security: SecurityPolicy{
			KnownAdvisory: ActionReview,
			Unknown:       ActionReview,
		},
		Dependencies: DependencyPolicy{
			Unknown:   ActionReview,
			MaxDirect: nil,
		},
		Maintenance: MaintenancePolicy{
			Archived:            ActionReview,
			Deprecated:          ActionReview,
			Stale:               ActionReview,
			Unknown:             ActionReview,
			MaxDaysSincePush:    nil,
			MaxDaysSinceRelease: nil,
		},
		Source: SourcePolicy{
			RequireRevisionFor: []model.ReuseMode{
				model.ReuseCopy,
				model.ReuseDependency,
				model.ReuseAdapt,
			},
			MissingRevision: ActionReview,
		},
		Selection: SelectionPolicy{MaxOptions: 3},
	}
}
