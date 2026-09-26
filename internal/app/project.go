package app

import (
	"errors"
	"time"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/discovery/providers/github"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/project"
)

// ErrStoreWithoutProjectContext is returned when a store cannot support
// project-context commands. The PostgreSQL store always can; a narrower test
// double may not, and the failure is reported rather than silently degrading
// to project-agnostic behaviour.
var ErrStoreWithoutProjectContext = errors.New("app: store does not provide project context")

// NewProjectService builds the project-context service over the shared store.
//
// The GitHub client it constructs is deliberately UNAUTHENTICATED: a token
// that can read private content would make Packet 10 able to reach material
// Packet 13 owns. A private or missing repository therefore fails as
// unsupported rather than becoming readable.
func NewProjectService(store Store, clock func() time.Time) (*project.Service, error) {
	repository, ok := store.(project.Repository)
	if !ok {
		return nil, ErrStoreWithoutProjectContext
	}
	return project.NewService(repository, project.Options{
		Clock: clock,
		NewGitHubClient: func() project.GitHubClient {
			return github.NewClient("")
		},
	}), nil
}
