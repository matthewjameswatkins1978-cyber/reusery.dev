// Command mcpcontract generates or verifies the checked-in Reusery MCP tool
// contract.
//
// The snapshot is produced by connecting a real MCP client over the official
// SDK's in-memory transports and reading tools/list, so it can never describe
// tools the server does not actually register:
//
//	go run ./cmd/mcpcontract -write mcp/reusery-tools-v1.json
//	go run ./cmd/mcpcontract -check  mcp/reusery-tools-v1.json
//
// It requires no PostgreSQL, no model key, no provider token and no network.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/mcpserver"
)

func main() {
	write := flag.String("write", "", "write the generated tool contract to this path")
	check := flag.String("check", "", "verify this path matches the generated tool contract")
	flag.Parse()

	if err := run(*write, *check); err != nil {
		fmt.Fprintln(os.Stderr, "mcpcontract:", err)
		os.Exit(1)
	}
}

func run(write, check string) error {
	if (write == "") == (check == "") {
		return errors.New("exactly one of -write or -check is required")
	}

	document, err := generate()
	if err != nil {
		return err
	}

	if write != "" {
		if dir := filepath.Dir(write); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create output directory: %w", err)
			}
		}
		if err := os.WriteFile(write, document, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", write, err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", write, len(document))
		return nil
	}

	checked, err := os.ReadFile(check)
	if err != nil {
		return fmt.Errorf("read %s: %w", check, err)
	}
	if !bytes.Equal(checked, document) {
		return fmt.Errorf("%s is out of date; regenerate it with: go run ./cmd/mcpcontract -write %s", check, check)
	}
	fmt.Printf("%s matches the registered MCP tool surface\n", check)
	return nil
}

func generate() ([]byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	snapshot, err := mcpserver.Contract(ctx, mcpserver.Dependencies{})
	if err != nil {
		return nil, err
	}
	return snapshot.Bytes()
}
