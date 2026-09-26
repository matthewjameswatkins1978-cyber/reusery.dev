package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// projectIDPrefix is the domain prefix for project identities. Including it in
// the hashed input keeps a project id from ever colliding with any other
// Reusery identifier.
const projectIDPrefix = "reusery-project-go-v1\n"

// canonicaliseFingerprint returns a deterministic copy of a fingerprint:
// modules by module_path, requirements by module_path then version,
// replacements by old_module_path then old_version.
//
// ObservedAt, storage IDs, the local root, warnings and map iteration order
// are all outside the Fingerprint struct, so they can never enter the hash.
func canonicaliseFingerprint(f Fingerprint) Fingerprint {
	out := Fingerprint{
		SchemaVersion: f.SchemaVersion,
		Language:      f.Language,
		Modules:       append([]GoModule(nil), f.Modules...),
	}
	sort.SliceStable(out.Modules, func(i, j int) bool {
		return out.Modules[i].ModulePath < out.Modules[j].ModulePath
	})
	for index := range out.Modules {
		module := &out.Modules[index]
		module.Requirements = append([]GoRequirement(nil), module.Requirements...)
		sort.SliceStable(module.Requirements, func(i, j int) bool {
			if module.Requirements[i].ModulePath != module.Requirements[j].ModulePath {
				return module.Requirements[i].ModulePath < module.Requirements[j].ModulePath
			}
			return module.Requirements[i].Version < module.Requirements[j].Version
		})
		module.Replacements = append([]GoReplacement(nil), module.Replacements...)
		sort.SliceStable(module.Replacements, func(i, j int) bool {
			if module.Replacements[i].OldModulePath != module.Replacements[j].OldModulePath {
				return module.Replacements[i].OldModulePath < module.Replacements[j].OldModulePath
			}
			return module.Replacements[i].OldVersion < module.Replacements[j].OldVersion
		})
	}
	return out
}

// FingerprintHash returns the SHA-256 of the canonical fingerprint JSON, as a
// lowercase hexadecimal digest. The same manifest facts always produce the
// same digest; any changed dependency, version or replacement changes it.
func FingerprintHash(f Fingerprint) (string, error) {
	encoded, err := json.Marshal(canonicaliseFingerprint(f))
	if err != nil {
		return "", fmt.Errorf("project: canonicalise fingerprint: %w", err)
	}
	return sha256Hex(encoded), nil
}

// ModulePaths returns the sorted unique module paths of a fingerprint. This is
// the identity input: deliberately independent of versions so a dependency bump
// does not create a different project.
func ModulePaths(f Fingerprint) []string {
	seen := make(map[string]struct{}, len(f.Modules))
	out := make([]string, 0, len(f.Modules))
	for _, module := range f.Modules {
		if _, exists := seen[module.ModulePath]; exists {
			continue
		}
		seen[module.ModulePath] = struct{}{}
		out = append(out, module.ModulePath)
	}
	sort.Strings(out)
	return out
}

// ProjectID derives the durable project identity from module identity alone.
//
// Two checkouts of the same module or workspace therefore identify as the
// same logical project, which is exactly why no random UUID and no filesystem
// path is ever used.
func ProjectID(f Fingerprint) string {
	return "project/go/" + sha256Hex([]byte(projectIDPrefix+strings.Join(ModulePaths(f), "\n")))
}

// ContextHash returns the SHA-256 of the canonical context JSON.
func ContextHash(c Context) (string, error) {
	encoded, err := json.Marshal(canonicaliseContext(c))
	if err != nil {
		return "", fmt.Errorf("project: canonicalise context: %w", err)
	}
	return sha256Hex(encoded), nil
}

// canonicaliseContext sorts active preferences by storage id so a snapshot
// hashes identically however the rows arrived.
func canonicaliseContext(c Context) Context {
	out := c
	out.Preferences = append([]PreferenceEffect(nil), c.Preferences...)
	sort.SliceStable(out.Preferences, func(i, j int) bool {
		return out.Preferences[i].PreferenceID < out.Preferences[j].PreferenceID
	})
	return out
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// DisplayName returns the cosmetic display name for a fingerprint: the final
// path component for a single module, or a deterministic compact label for a
// workspace. It is not identity.
func DisplayName(f Fingerprint) string {
	if len(f.Modules) == 1 {
		path := f.Modules[0].ModulePath
		if index := strings.LastIndex(path, "/"); index >= 0 && index+1 < len(path) {
			return path[index+1:]
		}
		return path
	}
	if len(f.Modules) == 0 {
		return "Empty Go project"
	}
	return fmt.Sprintf("Go workspace (%d modules)", len(f.Modules))
}
