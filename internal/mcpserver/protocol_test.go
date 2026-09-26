package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectPair starts the real server and returns both sessions so a test can
// assert what was negotiated, not merely that a connection happened.
func connectPair(t *testing.T, deps Dependencies, clientOptions *mcp.ClientSessionOptions) (*mcp.ClientSession, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	server := NewServer(deps)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "protocol-test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, clientOptions)
	if err != nil {
		cancel()
		t.Fatalf("connect client: %v", err)
	}
	return clientSession, func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
		cancel()
	}
}

// TestProtocolNegotiatesCurrentRevision proves the server speaks the current
// MCP revision the SDK advertises, without pinning it manually.
func TestProtocolNegotiatesCurrentRevision(t *testing.T) {
	supported := mcp.SupportedProtocolVersions()
	if len(supported) == 0 || supported[0] != "2026-07-28" {
		t.Fatalf("SDK supported versions = %v, want 2026-07-28 first", supported)
	}

	client, cleanup := connectPair(t, Dependencies{}, nil)
	defer cleanup()

	if got := client.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Errorf("negotiated protocol version = %q, want 2026-07-28", got)
	}
	result, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) != 12 {
		t.Errorf("tools = %d, want 12", len(result.Tools))
	}
}

// TestLegacyProtocolRevisionStillWorks forces an older supported revision so
// compatibility is proven by execution rather than assumed. Reusery does not
// implement protocol negotiation itself; the official SDK does.
func TestLegacyProtocolRevisionStillWorks(t *testing.T) {
	client, cleanup := connectPair(t, Dependencies{},
		&mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	defer cleanup()

	if got := client.InitializeResult().ProtocolVersion; got != "2025-11-25" {
		t.Errorf("negotiated protocol version = %q, want 2025-11-25", got)
	}
	result, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools on the legacy revision: %v", err)
	}
	if len(result.Tools) != 12 {
		t.Errorf("tools = %d, want 12", len(result.Tools))
	}
}

// TestFullToolConversation runs the whole agent-facing sequence over the real
// protocol: catalog, resolve, refine, inspect evidence, inspect resolution and
// report outcome, then closes cleanly.
func TestFullToolConversationOverMCP(t *testing.T) {
	f := newFixture(false)
	session, cleanup := connectPair(t, f.deps, nil)
	defer cleanup()

	tools, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 12 {
		t.Fatalf("tools = %d, want 12", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name == "reusery_normalize" {
			t.Fatal("reusery_normalize must not be exposed to agents")
		}
	}

	_, catalog := callTool(t, session, ToolCatalog, CatalogInput{})
	if catalog["primitives"] == nil {
		t.Fatalf("catalog returned no primitives: %v", catalog)
	}

	_, resolved := callTool(t, session, ToolResolve, f.resolveRequest(
		CandidateRef{SpecimenID: fixtureEligible, ReuseMode: "dependency"}))
	if resolved["status"] != "resolved" {
		t.Fatalf("status = %v, want resolved", resolved["status"])
	}
	resolutionID := resolved["resolution_id"].(float64)

	_, refined := callTool(t, session, ToolRefine, RefineInput{
		PrimitiveID: fixturePrimitive,
		ContractID:  fixtureContract,
		Candidates: []CandidateRef{
			{SpecimenID: fixtureEligible, ReuseMode: "dependency"},
			{SpecimenID: fixtureLight, ReuseMode: "dependency"},
		},
		Feedback: []policyFeedback{{CandidateID: fixtureEligible, Reason: feedbackNotQuite}},
	})
	if refined["selected"] == nil {
		t.Fatalf("refine produced no selection: %v", refined)
	}
	applied := refined["applied_feedback"].([]any)
	if len(applied) != 1 {
		t.Errorf("applied_feedback = %v", applied)
	}

	_, page := callTool(t, session, ToolInspectEvidence, EvidenceInput{
		SubjectID: fixtureEligible, Limit: 3,
	})
	if page["evidence"] == nil {
		t.Fatalf("evidence page missing: %v", page)
	}
	if page["next_after"] == nil {
		t.Error("expected a continuation key on a partial page")
	}

	_, stored := callTool(t, session, ToolInspectResolution, InspectResolutionInput{
		ResolutionID: int64(resolutionID),
	})
	if stored["outcome_feedback"] == nil {
		t.Errorf("outcome_feedback missing: %v", stored)
	}

	_, recorded := callTool(t, session, ToolReportOutcome, OutcomeInput{
		ResolutionID: int64(resolutionID),
		Kind:         "adopted",
		Note:         "used as the parser entry point",
	})
	if recorded["kind"] != "adopted" {
		t.Errorf("kind = %v", recorded["kind"])
	}
}

// TestToolErrorIsVisibleToTheModel proves a failure is reported with MCP
// tool-error semantics rather than as a protocol error the model cannot read.
func TestToolErrorIsVisibleToTheModel(t *testing.T) {
	f := newFixture(false)
	session, cleanup := connectPair(t, f.deps, nil)
	defer cleanup()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolInspectResolution,
		Arguments: InspectResolutionInput{ResolutionID: 999},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected MCP tool-error semantics")
	}
	text := textOf(result)
	if !strings.Contains(text, CodeNotFound+":") {
		t.Errorf("tool error text = %q, want a stable code prefix", text)
	}
}

// TestCancellationReachesTheToolHandler starts a blocking tool call, cancels
// the client's context and requires the underlying handler to observe the
// cancellation. The test is bounded so a leaked goroutine fails loudly.
func TestCancellationReachesTheToolHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	entered := make(chan struct{}, 1)
	observed := make(chan error, 1)

	server := mcp.NewServer(&mcp.Implementation{Name: "canceller"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "block",
		Description: "blocks until cancelled",
	}, func(callCtx context.Context, _ *mcp.CallToolRequest, in struct {
		Name string `json:"name,omitempty"`
	}) (*mcp.CallToolResult, struct {
		OK bool `json:"ok"`
	}, error) {
		entered <- struct{}{}
		select {
		case <-callCtx.Done():
			observed <- callCtx.Err()
			return nil, struct {
				OK bool `json:"ok"`
			}{OK: false}, callCtx.Err()
		case <-time.After(10 * time.Second):
			observed <- nil
			return nil, struct {
				OK bool `json:"ok"`
			}{OK: true}, nil
		}
	})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "canceller-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	callCtx, cancelCall := context.WithCancel(ctx)
	go func() {
		select {
		case <-entered:
			cancelCall()
		case <-ctx.Done():
		}
	}()

	_, callErr := clientSession.CallTool(callCtx, &mcp.CallToolParams{
		Name:      "block",
		Arguments: map[string]any{"name": "x"},
	})
	if callErr == nil {
		t.Error("expected the cancelled call to return an error")
	}

	select {
	case err := <-observed:
		if err == nil {
			t.Fatal("the tool handler was never cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation never reached the tool handler")
	}
}
