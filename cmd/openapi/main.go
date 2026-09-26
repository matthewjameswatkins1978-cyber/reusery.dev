// Command openapi generates or verifies the checked-in Reusery API v1
// contract.
//
// The document is produced from the same Huma registrations the server uses,
// so it can never drift from the running API without CI noticing:
//
//	go run ./cmd/openapi -write openapi/reusery-v1.json
//	go run ./cmd/openapi -check openapi/reusery-v1.json
//
// It requires no PostgreSQL, no OpenAI key, no GitHub token and no network
// access: route registration is separated from live service construction.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/api"
)

func main() {
	write := flag.String("write", "", "write the generated contract to this path")
	check := flag.String("check", "", "verify this path matches the generated contract")
	flag.Parse()

	if err := run(*write, *check); err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
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
		if err := os.MkdirAll(filepath.Dir(write), 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
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
		return fmt.Errorf("%s is out of date; regenerate it with: go run ./cmd/openapi -write %s", check, check)
	}
	fmt.Printf("%s matches the registered API\n", check)
	return nil
}

// generate renders the registered OpenAPI 3.1 document deterministically.
func generate() ([]byte, error) {
	handler := api.NewHandler(api.Dependencies{})
	compact, err := json.Marshal(handler.OpenAPI())
	if err != nil {
		return nil, fmt.Errorf("marshal openapi document: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, fmt.Errorf("indent openapi document: %w", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}
