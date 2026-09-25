// Package version exposes application build metadata.
//
// The variables default to development values. Release builds can override
// them with Go linker flags, for example:
//
//	go build -ldflags "-X github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version.Version=1.0.0 -X github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version.Commit=abc123 -X github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/version.BuildTime=2026-09-25T00:00:00Z" ./cmd/reusery
package version

// Name is the application name.
const Name = "reusery"

// Build metadata. Overridden at link time for release builds (see above).
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

// Info returns the build metadata as key/value pairs suitable for logging.
func Info() map[string]string {
	return map[string]string{
		"name":       Name,
		"version":    Version,
		"commit":     Commit,
		"build_time": BuildTime,
	}
}
