package mcpserver

import (
	"os"
	"strings"
	"testing"
)

// productionSources returns every non-test Go file in this package.
func productionSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = string(data)
	}
	if len(out) == 0 {
		t.Fatal("no production sources found")
	}
	return out
}

// TestNoRemoteMCPTransportExists proves Packet 9 is stdio only.
//
// There is no HTTP listener, no /mcp route and no streamable or SSE transport
// anywhere in the production MCP package. Remote MCP stays deferred until
// Packet 13 identity and Packet 15 abuse controls exist, and it must not be
// smuggled in through Packet 8's unauthenticated HTTP server either.
func TestNoRemoteMCPTransportExists(t *testing.T) {
	forbidden := []string{
		"ListenAndServe",
		"http.Listen",
		"streamable",
		"/mcp/sse",
		`"net/http"`,
	}
	for name, source := range productionSources(t) {
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				t.Errorf("%s contains %q: Packet 9 must stay stdio only", name, token)
			}
		}
	}
}

// TestMCPPackageBoundary keeps the transport adapter from reaching into
// storage, the HTTP surface or the model provider.
func TestMCPPackageBoundary(t *testing.T) {
	forbidden := []string{
		`"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/api"`,
		`"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"`,
		`"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent"`,
		`"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/intent/providers/openai"`,
		`"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/cli"`,
	}
	for name, source := range productionSources(t) {
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				t.Errorf("%s imports %s: MCP is a transport adapter, not a new owner of behaviour", name, token)
			}
		}
	}
}

// TestNoModelProviderInMCP is the customer-cost guard: a calling agent is
// already the reasoning surface, so the MCP server must never construct a
// second model just to translate that agent's own structured input.
func TestNoModelProviderInMCP(t *testing.T) {
	forbidden := []string{"Normalizer", "Normalize(", "openai", "OpenAI", "REUSERY_OPENAI_API_KEY"}
	for name, source := range productionSources(t) {
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				t.Errorf("%s references %q: the MCP server must not construct a model provider", name, token)
			}
		}
	}
}

// TestMCPServerNeverWritesToStdout protects the stdio transport. A single
// stray line on stdout corrupts an MCP frame, so nothing in this package may
// reach for the process standard output: operational logging goes to stderr.
func TestMCPServerNeverWritesToStdout(t *testing.T) {
	forbidden := []string{"os.Stdout", "fmt.Print", "println(", "log.Print", "os.Stdin"}
	for name, source := range productionSources(t) {
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				t.Errorf("%s references %q: stdout is reserved for MCP frames", name, token)
			}
		}
	}
}
