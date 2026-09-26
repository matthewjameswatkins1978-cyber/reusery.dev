package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Snapshot is the durable part of the MCP tool contract.
//
// Volatile values — session ids, build metadata, timestamps — are deliberately
// excluded so the drift gate only fires when the contract itself changes.
type Snapshot struct {
	ContractVersion  int            `json:"contract_version"`
	ServerName       string         `json:"server_name"`
	ProtocolVersions []string       `json:"protocol_versions"`
	Tools            []SnapshotTool `json:"tools"`
}

// SnapshotTool is one registered tool as a client sees it.
type SnapshotTool struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Annotations  *mcp.ToolAnnotations `json:"annotations,omitempty"`
	InputSchema  json.RawMessage      `json:"input_schema"`
	OutputSchema json.RawMessage      `json:"output_schema"`
}

// Contract reads the live tool list from a real MCP client over the official
// SDK's in-memory transports.
//
// It performs no I/O beyond that in-process conversation: no database, no
// model key, no provider token and no network. That is what lets a CI job
// verify the checked-in contract without any credentials.
func Contract(ctx context.Context, deps Dependencies) (Snapshot, error) {
	server := NewServer(deps)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("connect server: %w", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "contract"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("connect client: %w", err)
	}
	defer func() { _ = clientSession.Close() }()

	tools, err := listAllTools(ctx, clientSession)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		ContractVersion:  ContractVersion,
		ServerName:       ServerName,
		ProtocolVersions: mcp.SupportedProtocolVersions(),
		Tools:            make([]SnapshotTool, 0, len(tools)),
	}
	for _, tool := range tools {
		input, err := rawSchema(tool.InputSchema)
		if err != nil {
			return Snapshot{}, fmt.Errorf("tool %s input schema: %w", tool.Name, err)
		}
		output, err := rawSchema(tool.OutputSchema)
		if err != nil {
			return Snapshot{}, fmt.Errorf("tool %s output schema: %w", tool.Name, err)
		}
		snapshot.Tools = append(snapshot.Tools, SnapshotTool{
			Name:         tool.Name,
			Description:  tool.Description,
			Annotations:  tool.Annotations,
			InputSchema:  input,
			OutputSchema: output,
		})
	}
	sort.Slice(snapshot.Tools, func(i, j int) bool {
		return snapshot.Tools[i].Name < snapshot.Tools[j].Name
	})
	return snapshot, nil
}

// Bytes renders the snapshot as stable, indented JSON terminated by a newline.
// Object keys are sorted by encoding/json, so two runs over the same server
// agree byte for byte.
func (s Snapshot) Bytes() ([]byte, error) {
	compact, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal contract: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, fmt.Errorf("indent contract: %w", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}

// listAllTools follows pagination so a future tool-count increase is still
// captured rather than silently truncated.
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	cursor := ""
	for {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		out = append(out, result.Tools...)
		if result.NextCursor == "" {
			return out, nil
		}
		cursor = result.NextCursor
	}
}

// rawSchema renders a decoded schema back to deterministic JSON.
func rawSchema(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}
