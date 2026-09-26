//go:build integration

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/store/postgres"
)

// buildReusery compiles the real CLI so the test exercises the actual
// `reusery mcp` entry point rather than an in-process stand-in.
func buildReusery(t *testing.T) string {
	t.Helper()
	name := "reusery"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", binary, "./cmd/reusery")
	build.Dir = mcpRepoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build reusery: %v\n%s", err, output)
	}
	return binary
}

// TestStdioExecutableConversation is the most important Packet 9 integration
// test: the real binary, the real stdio transport, a real PostgreSQL, and the
// official SDK client.
//
// A stray line on stdout would corrupt the JSON-RPC framing and the handshake
// would fail, so a successful conversation is itself the stdout-purity proof.
// External operations are disabled and REUSERY_OPENAI_API_KEY is deliberately
// absent from the child environment.
func TestStdioExecutableConversation(t *testing.T) {
	pool, databaseURL := startMCPDatabaseWithURL(t)
	store := postgres.NewStore(pool)
	seedMCPFixture(t, store)

	binary := buildReusery(t)

	// The local project root is configured once, at startup: a tool call can
	// never point the server at another part of the filesystem.
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"),
		[]byte("module example.com/stdio-widget\n\ngo 1.27.1\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	server := exec.Command(binary, "mcp", "--project-root", projectRoot)
	server.Env = []string{
		"REUSERY_DATABASE_URL=" + databaseURL,
		"REUSERY_MCP_ENABLE_EXTERNAL_OPERATIONS=false",
		"REUSERY_LOG_LEVEL=info",
		"PATH=" + os.Getenv("PATH"),
		"SystemRoot=" + os.Getenv("SystemRoot"),
		"COMSPEC=" + os.Getenv("COMSPEC"),
	}
	var stderr bytes.Buffer
	server.Stderr = &stderr
	// stdout is left for CommandTransport: nothing else may write to it.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transport := &mcp.CommandTransport{Command: server}
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect over stdio: %v\nstderr=%s", err, stderr.String())
	}

	// 1. tool discovery without reading any website
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 12 {
		t.Errorf("tools = %d, want 12", len(tools.Tools))
	}

	// 2. a local project scan uses the configured root and never reports it
	_, scanned := callTool(t, session, ToolProjectScan, ProjectScanInput{})
	if scanned["source_kind"] != "local" {
		t.Fatalf("source kind = %v, want local", scanned["source_kind"])
	}
	projectID, _ := scanned["project_id"].(string)
	if !strings.HasPrefix(projectID, "project/go/") {
		t.Fatalf("project id = %q", projectID)
	}
	if encoded, err := json.Marshal(scanned); err != nil {
		t.Fatalf("encode scan: %v", err)
	} else if bytes.Contains(encoded, []byte(projectRoot)) {
		t.Errorf("the scan result contains the configured root: %s", encoded)
	}

	// 3. capability discovery
	_, catalogResult := callTool(t, session, ToolCatalog, CatalogInput{})
	if catalogResult["primitives"] == nil {
		t.Fatalf("catalog returned nothing: %v", catalogResult)
	}
	_, detail := callTool(t, session, ToolCatalog, CatalogInput{PrimitiveID: fixturePrimitive})
	if detail["contract"] == nil {
		t.Fatalf("catalog detail returned nothing: %v", detail)
	}

	// 4. resolve a deterministic fixture
	pol := permissivePolicyForFixture()
	_, decision := callTool(t, session, ToolResolve, ResolveInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: completeID, ReuseMode: "dependency"},
		},
		Policy: &pol,
	})
	if decision["status"] != "resolved" || decision["outcome"] != "depend" {
		t.Fatalf("decision = %v", decision)
	}
	resolutionID := int64(decision["resolution_id"].(float64))

	// 5. inspect the remembered resolution
	_, stored := callTool(t, session, ToolInspectResolution,
		InspectResolutionInput{ResolutionID: resolutionID})
	if stored["resolution"] == nil {
		t.Fatalf("resolution missing: %v", stored)
	}
	if feedback := stored["outcome_feedback"].([]any); len(feedback) != 0 {
		t.Errorf("expected no feedback yet, got %v", feedback)
	}

	// 6. report what actually happened
	_, recorded := callTool(t, session, ToolReportOutcome, OutcomeInput{
		ResolutionID: resolutionID, Kind: "adopted", Note: "wired into the parser",
	})
	if recorded["kind"] != "adopted" {
		t.Errorf("kind = %v", recorded["kind"])
	}

	// 7. inspect again: the history is now visible
	_, reloaded := callTool(t, session, ToolInspectResolution,
		InspectResolutionInput{ResolutionID: resolutionID})
	events := reloaded["outcome_feedback"].([]any)
	if len(events) != 1 {
		t.Fatalf("outcome events = %d, want 1", len(events))
	}
	if events[0].(map[string]any)["kind"] != "adopted" {
		t.Errorf("event = %v", events[0])
	}

	// 8. external operations are off: no provider call can happen
	toolResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      ToolDiscover,
		Arguments: discoveryProfileArguments(),
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !toolResult.IsError {
		t.Error("discover must fail while external operations are disabled")
	}
	if text := textOf(toolResult); !strings.Contains(text, CodeExternalOperations+":") {
		t.Errorf("tool error = %q", text)
	}

	// 9. close cleanly: the child must exit without a protocol traceback
	if err := session.Close(); err != nil {
		t.Errorf("close session: %v", err)
	}
	waitForExit(t, server)

	output := stderr.String()
	if strings.Contains(output, `{"jsonrpc"`) {
		t.Errorf("stderr carried protocol traffic:\n%s", output)
	}
	if !strings.Contains(output, "starting mcp server") {
		t.Errorf("stderr missing the bounded startup log:\n%s", output)
	}
}

// discoveryProfileArguments is a valid but gated discovery request.
func discoveryProfileArguments() map[string]any {
	return map[string]any{
		"schema_version": 1,
		"primitive_id":   fixturePrimitive,
		"contract_id":    fixtureContract,
		"providers": []map[string]any{
			{"id": "pkg.go.dev", "queries": []map[string]any{{"text": "subprocess", "limit": 1}}},
		},
	}
}

// waitForExit requires the child process to terminate on its own after its
// stdin is closed. It is bounded so a leaked process fails loudly.
func waitForExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil {
			if !cmd.ProcessState.Success() {
				t.Errorf("reusery mcp exited with code %d", cmd.ProcessState.ExitCode())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatal("reusery mcp did not exit after its input stream closed")
}
