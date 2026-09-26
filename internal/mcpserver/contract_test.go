package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkedInContract is the generated, checked-in MCP tool contract.
const checkedInContract = "mcp/reusery-tools-v1.json"

func loadCheckedInContract(t *testing.T) []byte {
	t.Helper()
	document, err := os.ReadFile(filepath.Join("..", "..", checkedInContract))
	if err != nil {
		t.Fatalf("read %s: %v", checkedInContract, err)
	}
	return document
}

func generateContract(t *testing.T) []byte {
	t.Helper()
	snapshot, err := Contract(context.Background(), Dependencies{})
	if err != nil {
		t.Fatalf("generate contract: %v", err)
	}
	document, err := snapshot.Bytes()
	if err != nil {
		t.Fatalf("render contract: %v", err)
	}
	return document
}

// TestCheckedInContractMatchesRegisteredTools is the MCP drift gate: CI fails
// if a tool name, description, schema or annotation changes without the
// contract being deliberately regenerated.
func TestCheckedInContractMatchesRegisteredTools(t *testing.T) {
	checked := loadCheckedInContract(t)
	generated := generateContract(t)
	if !bytes.Equal(checked, generated) {
		t.Fatalf("%s is out of date; regenerate with: go run ./cmd/mcpcontract -write %s",
			checkedInContract, checkedInContract)
	}
}

// TestContractDescribesExactlyThePacket9Surface locks the tool list.
func TestContractDescribesExactlyThePacket9Surface(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal(loadCheckedInContract(t), &snapshot); err != nil {
		t.Fatalf("decode contract: %v", err)
	}

	if snapshot.ContractVersion != ContractVersion {
		t.Errorf("contract_version = %d, want %d", snapshot.ContractVersion, ContractVersion)
	}
	if snapshot.ServerName != ServerName {
		t.Errorf("server_name = %q, want %q", snapshot.ServerName, ServerName)
	}
	if len(snapshot.ProtocolVersions) == 0 {
		t.Error("protocol_versions is empty")
	}
	if snapshot.ProtocolVersions[0] != "2026-07-28" {
		t.Errorf("first protocol version = %q, want 2026-07-28", snapshot.ProtocolVersions[0])
	}

	want := ToolNames()
	if len(snapshot.Tools) != 12 {
		t.Fatalf("tools = %d, want 12", len(snapshot.Tools))
	}
	if len(want) != 12 {
		t.Fatalf("ToolNames() = %d entries, want 12", len(want))
	}
	for i, name := range want {
		if snapshot.Tools[i].Name != name {
			t.Errorf("tool %d = %q, want %q", i, snapshot.Tools[i].Name, name)
		}
	}
	// Packet 9 names are frozen: adding Packet 10 tools must not rename them.
	packetNine := []string{
		ToolCatalog, ToolDiscover, ToolEnrich, ToolInspectEvidence,
		ToolInspectResolution, ToolRefine, ToolReportOutcome, ToolResolve,
	}
	for _, name := range packetNine {
		found := false
		for _, tool := range snapshot.Tools {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Packet 9 tool %q is missing", name)
		}
	}
	for _, name := range []string{ToolProjectScan, ToolProjectContext, ToolProjectRemember, ToolProjectForget} {
		found := false
		for _, tool := range snapshot.Tools {
			if tool.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("project tool %q is missing", name)
		}
	}
	for _, tool := range snapshot.Tools {
		if strings.HasSuffix(tool.Name, "_normalize") {
			t.Errorf("%q must not be exposed: an agent caller needs no second model", tool.Name)
		}
		if strings.Contains(tool.Name, "health") || strings.Contains(tool.Name, "ready") ||
			strings.Contains(tool.Name, "openapi") {
			t.Errorf("%q must not be exposed: the surface is resolver-native, not an HTTP mirror", tool.Name)
		}
		if len(tool.Description) == 0 {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if len(tool.OutputSchema) == 0 || string(tool.OutputSchema) == "null" {
			t.Errorf("tool %q has no structured output schema", tool.Name)
		}
		if tool.Annotations == nil {
			t.Errorf("tool %q has no annotations", tool.Name)
		}
	}
}

// TestContractAnnotationsAreAccurate pins the annotation table from the
// packet. Annotations are hints, but they must not lie.
func TestContractAnnotationsAreAccurate(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal(loadCheckedInContract(t), &snapshot); err != nil {
		t.Fatalf("decode contract: %v", err)
	}

	want := map[string]struct {
		readOnly   bool
		openWorld  bool
		idempotent bool
	}{
		ToolCatalog:           {readOnly: true, openWorld: false},
		ToolDiscover:          {readOnly: false, openWorld: true},
		ToolEnrich:            {readOnly: false, openWorld: true},
		ToolResolve:           {readOnly: false, openWorld: false},
		ToolRefine:            {readOnly: false, openWorld: false},
		ToolInspectEvidence:   {readOnly: true, openWorld: false},
		ToolInspectResolution: {readOnly: true, openWorld: false},
		ToolReportOutcome:     {readOnly: false, openWorld: false},
		ToolProjectScan:       {readOnly: false, openWorld: true},
		ToolProjectContext:    {readOnly: true, openWorld: false},
		ToolProjectRemember:   {readOnly: false, openWorld: false, idempotent: true},
		ToolProjectForget:     {readOnly: false, openWorld: false, idempotent: true},
	}
	if len(want) != len(snapshot.Tools) {
		t.Fatalf("annotation table covers %d tools, contract has %d", len(want), len(snapshot.Tools))
	}
	for _, tool := range snapshot.Tools {
		expected, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		if tool.Annotations.ReadOnlyHint != expected.readOnly {
			t.Errorf("%s readOnlyHint = %v, want %v", tool.Name, tool.Annotations.ReadOnlyHint, expected.readOnly)
		}
		if tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s has no openWorldHint", tool.Name)
			continue
		}
		if *tool.Annotations.OpenWorldHint != expected.openWorld {
			t.Errorf("%s openWorldHint = %v, want %v", tool.Name, *tool.Annotations.OpenWorldHint, expected.openWorld)
		}
		if !expected.readOnly {
			if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
				t.Errorf("%s must declare destructiveHint=false", tool.Name)
			}
			if tool.Annotations.IdempotentHint != expected.idempotent {
				t.Errorf("%s idempotentHint = %v, want %v",
					tool.Name, tool.Annotations.IdempotentHint, expected.idempotent)
			}
		}
		if expected.idempotent && tool.Annotations.ReadOnlyHint {
			t.Errorf("%s cannot be both read-only and idempotent-hinted", tool.Name)
		}
	}
}

// forbiddenInputProperties are names that would turn the agent surface into a
// remote filesystem interface, a provider configuration endpoint or a place to
// put credentials. None of them may appear anywhere in an input schema.
var forbiddenInputProperties = []string{
	"root", "path", "manifest", "profile_file", "policy_file", "corpus_file",
	"base_url", "endpoint", "api_key", "apikey", "token", "password", "secret",
	"openai", "authorization",
	"directory", "directory_path", "dir", "cwd", "workspace", "repo_path",
	"local_path", "checkout", "source_path",
}

// TestContractHasNoFilesystemOrCredentialInputs guards two standing rules at
// once: no remote filesystem surface, and no way to inject a provider host or
// credential through a tool argument.
func TestContractHasNoFilesystemOrCredentialInputs(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal(loadCheckedInContract(t), &snapshot); err != nil {
		t.Fatalf("decode contract: %v", err)
	}

	for _, tool := range snapshot.Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("tool %s input schema: %v", tool.Name, err)
		}
		for _, property := range collectPropertyNames(schema) {
			for _, forbidden := range forbiddenInputProperties {
				if property == forbidden {
					t.Errorf("tool %s accepts a %q input", tool.Name, property)
				}
			}
		}
	}
}

// TestContractKeepsProjectContextOptional is the additive rule for the agent
// surface: resolve and refine accept an optional project_id, and omitting it
// is exactly the Packet 9 call. project_id is never required, and the project
// tools never take a filesystem path.
func TestContractKeepsProjectContextOptional(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal(loadCheckedInContract(t), &snapshot); err != nil {
		t.Fatalf("decode contract: %v", err)
	}

	byName := map[string]map[string]any{}
	for _, tool := range snapshot.Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("tool %s input schema: %v", tool.Name, err)
		}
		byName[tool.Name] = schema
	}

	for _, name := range []string{ToolResolve, ToolRefine} {
		schema, ok := byName[name]
		if !ok {
			t.Fatalf("%s missing from the contract", name)
		}
		properties, _ := schema["properties"].(map[string]any)
		if _, present := properties["project_id"]; !present {
			t.Errorf("%s does not accept project_id", name)
		}
		for _, required := range toSlice(schema["required"]) {
			if required == "project_id" {
				t.Errorf("%s requires project_id; the Packet 9 call must stay valid", name)
			}
		}
	}

	scan, ok := byName[ToolProjectScan]
	if !ok {
		t.Fatalf("%s missing from the contract", ToolProjectScan)
	}
	if required := toSlice(scan["required"]); len(required) != 0 {
		t.Errorf("%s requires %v; a local scan needs no arguments", ToolProjectScan, required)
	}
	for _, name := range []string{ToolProjectContext, ToolProjectRemember, ToolProjectForget} {
		properties, _ := byName[name]["properties"].(map[string]any)
		if _, present := properties["project_id"]; !present {
			t.Errorf("%s does not accept project_id", name)
		}
	}
}

// toSlice reads a JSON schema "required" array.
func toSlice(value any) []string {
	entries, _ := value.([]any)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if text, ok := entry.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// TestContractExposesNoInternalSchemaNames keeps storage implementation
// details out of a public machine contract.
func TestContractExposesNoInternalSchemaNames(t *testing.T) {
	document := loadCheckedInContract(t)
	for _, forbidden := range []string{
		"postgres.", "sqlc.", "pgx", "GetResolutionRow", "UpsertPrimitive",
		"internal/store/postgres", "REUSERY_DATABASE_URL", "REUSERY_OPENAI_API_KEY",
		"REUSERY_GITHUB_TOKEN", "sk-", "ghp_",
	} {
		if bytes.Contains(document, []byte(forbidden)) {
			t.Errorf("contract contains %q", forbidden)
		}
	}
}

// collectPropertyNames walks a JSON schema and returns every declared
// property name, including nested ones.
func collectPropertyNames(schema map[string]any) []string {
	var out []string
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, value := range properties {
			out = append(out, name)
			if nested, ok := value.(map[string]any); ok {
				out = append(out, collectPropertyNames(nested)...)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		out = append(out, collectPropertyNames(items)...)
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		for _, entry := range anyOf {
			if nested, ok := entry.(map[string]any); ok {
				out = append(out, collectPropertyNames(nested)...)
			}
		}
	}
	return out
}
