package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generatedContract renders the registered document exactly the way
// cmd/openapi does.
func generatedContract(t *testing.T) []byte {
	t.Helper()
	handler := NewHandler(Dependencies{})
	compact, err := json.Marshal(handler.OpenAPI())
	if err != nil {
		t.Fatalf("marshal openapi: %v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		t.Fatalf("indent openapi: %v", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

func loadContract(t *testing.T, document []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(document, &out); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	return out
}

// requiredOperationIDs is the frozen API v1 operation id set.
var requiredOperationIDs = map[string]bool{
	"healthCheck":        false,
	"readyCheck":         false,
	"normalizeIntent":    false,
	"discoverCandidates": false,
	"enrichCandidates":   false,
	"resolveCandidates":  false,
	"refineResolution":   false,
	"getPrimitive":       false,
	"getContract":        false,
	"getSpecimen":        false,
	"listEvidence":       false,
	"getResolution":      false,
}

// requiredV1Paths is the frozen API v1 path set.
var requiredV1Paths = []string{
	"/health",
	"/ready",
	"/v1/normalize",
	"/v1/discover",
	"/v1/enrich",
	"/v1/resolve",
	"/v1/refine",
	"/v1/primitives",
	"/v1/contracts",
	"/v1/specimens",
	"/v1/evidence",
	"/v1/resolutions/{resolution_id}",
}

// requiredServedPaths are Huma-generated documentation routes. They are
// served at runtime but are infrastructure rather than API operations, so they
// are not listed under the document's own paths.
var requiredServedPaths = []string{
	"/openapi.json",
	"/openapi.yaml",
	"/openapi-3.0.json",
	"/openapi-3.0.yaml",
	"/docs",
	"/schemas/Problem.json",
}

func TestContractIsOpenAPI31(t *testing.T) {
	doc := loadContract(t, generatedContract(t))

	version, _ := doc["openapi"].(string)
	if !strings.HasPrefix(version, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x", version)
	}
	info := doc["info"].(map[string]any)
	if info["title"] != "Reusery API" {
		t.Errorf("info.title = %v, want Reusery API", info["title"])
	}
	if info["version"] != APIVersion {
		t.Errorf("info.version = %v, want %s", info["version"], APIVersion)
	}
	description := info["description"].(string)
	for _, phrase := range []string{
		"discovery is not verification",
		"REFERENCE is not contract satisfaction",
		"needs_verification is not a failure",
		"BUILD LOCALLY is a valid resolution outcome",
	} {
		if !strings.Contains(description, phrase) {
			t.Errorf("info.description is missing %q", phrase)
		}
	}
}

func TestEveryRequiredOperationIDExistsExactlyOnce(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	seen := map[string]int{}

	paths := doc["paths"].(map[string]any)
	for _, itemValue := range paths {
		item := itemValue.(map[string]any)
		for _, method := range []string{"get", "post", "put", "delete", "patch"} {
			raw, ok := item[method]
			if !ok {
				continue
			}
			operation := raw.(map[string]any)
			id, _ := operation["operationId"].(string)
			if id == "" {
				t.Errorf("%s %s has no operationId", method, itemValue)
				continue
			}
			seen[id]++
		}
	}

	for id := range requiredOperationIDs {
		if seen[id] != 1 {
			t.Errorf("operationId %q appears %d times, want exactly 1", id, seen[id])
		}
	}
	for id, count := range seen {
		if _, known := requiredOperationIDs[id]; !known && count > 0 && id != "" {
			// Generated documentation routes are not registered operations, so
			// anything else unexpected is a contract change.
			if !strings.HasPrefix(id, "get") {
				t.Errorf("unexpected operationId %q in the contract", id)
			}
		}
	}
}

func TestEveryRequiredPathIsPresent(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	paths := doc["paths"].(map[string]any)

	for _, path := range requiredV1Paths {
		if _, ok := paths[path]; !ok {
			t.Errorf("path %q is missing from the contract", path)
		}
	}
}

// TestDocumentationRoutesAreServed proves the generated documentation surface
// is reachable without a database, a provider or a model key.
func TestDocumentationRoutesAreServed(t *testing.T) {
	h := newTestHandler(t, testDependencies())

	for _, path := range requiredServedPaths {
		rec := call(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
			continue
		}
		if rec.Header().Get(headerAPIVersion) != APIVersion {
			t.Errorf("%s: missing Reusery-API-Version", path)
		}
	}
}

func TestTagsAreDeclared(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	raw, ok := doc["tags"]
	if !ok {
		t.Fatal("no top-level tags")
	}
	declared := map[string]bool{}
	for _, entry := range raw.([]any) {
		declared[entry.(map[string]any)["name"].(string)] = true
	}
	for _, tag := range []string{"Operations", "Intent", "Discovery", "Evidence", "Resolver", "Inspection"} {
		if !declared[tag] {
			t.Errorf("tag %q is not declared", tag)
		}
	}
}

func TestDecisionSchemasDocumentRequiredEnums(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)

	decision := schemas["DecisionResponse"].(map[string]any)
	status := decision["properties"].(map[string]any)["status"].(map[string]any)
	statusEnum := toStrings(status["enum"])
	if !containsAll(statusEnum, "resolved", "needs_verification") {
		t.Errorf("DecisionResponse.status enum = %v", statusEnum)
	}

	resolution := schemas["Resolution"].(map[string]any)
	outcome := resolution["properties"].(map[string]any)["outcome"].(map[string]any)
	outcomeEnum := toStrings(outcome["enum"])
	if !containsAll(outcomeEnum, "reuse", "adapt", "depend", "reference", "build_locally") {
		t.Errorf("Resolution.outcome enum = %v", outcomeEnum)
	}

	candidate := schemas["CandidateRef"].(map[string]any)
	mode := candidate["properties"].(map[string]any)["reuse_mode"].(map[string]any)
	modeEnum := toStrings(mode["enum"])
	if !containsAll(modeEnum, "copy", "dependency", "adapt", "reference") {
		t.Errorf("CandidateRef.reuse_mode enum = %v", modeEnum)
	}

	feedback := schemas["Feedback"].(map[string]any)
	reason := feedback["properties"].(map[string]any)["reason"].(map[string]any)
	reasonEnum := toStrings(reason["enum"])
	if !containsAll(reasonEnum, "not_quite", "too_many_dependencies", "licence_not_allowed",
		"avoid_dependency", "avoid_reference", "archived_project") {
		t.Errorf("Feedback.reason enum = %v", reasonEnum)
	}
}

func TestErrorSchemaCarriesCodeAndValidationDetail(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)

	problem := schemas["Problem"].(map[string]any)
	properties := problem["properties"].(map[string]any)
	for _, field := range []string{"status", "title", "detail", "code", "errors"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("Problem schema is missing %q", field)
		}
	}
	required := toStrings(problem["required"])
	if !containsAll(required, "status", "title", "detail", "code") {
		t.Errorf("Problem.required = %v, want status/title/detail/code", required)
	}
}

func TestResolveAndRefineDocumentBothDecisionStatuses(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	paths := doc["paths"].(map[string]any)

	for _, path := range []string{"/v1/resolve", "/v1/refine"} {
		operation := paths[path].(map[string]any)["post"].(map[string]any)
		responses := operation["responses"].(map[string]any)
		twoHundred, ok := responses["200"]
		if !ok {
			t.Fatalf("%s has no 200 response", path)
		}
		content := twoHundred.(map[string]any)["content"].(map[string]any)
		schema := content["application/json"].(map[string]any)["schema"].(map[string]any)
		ref, _ := schema["$ref"].(string)
		if !strings.HasSuffix(ref, "DecisionResponse") {
			t.Errorf("%s 200 schema = %q, want DecisionResponse", path, ref)
		}
		for _, status := range []string{"422", "500"} {
			if _, ok := responses[status]; !ok {
				t.Errorf("%s does not document %s", path, status)
			}
		}
	}
}

func TestNullableDecisionFieldsAreDocumentedAsNullOrObject(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	decision := schemas["DecisionResponse"].(map[string]any)
	properties := decision["properties"].(map[string]any)

	for _, field := range []string{"resolution", "selected"} {
		property := properties[field].(map[string]any)
		anyOf, ok := property["anyOf"].([]any)
		if !ok {
			t.Fatalf("%s is not documented as nullable: %v", field, property)
		}
		if len(anyOf) != 2 {
			t.Errorf("%s anyOf = %d entries, want 2", field, len(anyOf))
		}
		if _, ok := property["anyOf"].([]any)[1].(map[string]any)["type"]; !ok {
			t.Errorf("%s is missing the null alternative", field)
		}
	}
}

func TestRequestBodyLimitsAreDocumented(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	paths := doc["paths"].(map[string]any)

	want := map[string]string{
		"/v1/normalize": "16 KiB",
		"/v1/discover":  "64 KiB",
		"/v1/enrich":    "32 KiB",
		"/v1/resolve":   "256 KiB",
		"/v1/refine":    "256 KiB",
	}
	for path, limit := range want {
		description := paths[path].(map[string]any)["post"].(map[string]any)["description"].(string)
		if !strings.Contains(description, "Request body limit: "+limit) {
			t.Errorf("%s description does not document the %s body limit", path, limit)
		}
	}
}

func TestEvidencePaginationIsDocumented(t *testing.T) {
	doc := loadContract(t, generatedContract(t))
	operation := doc["paths"].(map[string]any)["/v1/evidence"].(map[string]any)["get"].(map[string]any)

	names := map[string]map[string]any{}
	for _, raw := range operation["parameters"].([]any) {
		parameter := raw.(map[string]any)
		names[parameter["name"].(string)] = parameter
	}
	for _, name := range []string{"subject_id", "limit", "cursor"} {
		if _, ok := names[name]; !ok {
			t.Errorf("parameter %q is not documented", name)
		}
	}
	limit := names["limit"]
	schema := limit["schema"].(map[string]any)
	if schema["default"] != float64(50) {
		t.Errorf("limit default = %v, want 50", schema["default"])
	}
	if schema["maximum"] != float64(100) {
		t.Errorf("limit maximum = %v, want 100", schema["maximum"])
	}
}

// TestContractHasNoInternalTypeNames keeps storage and transport concerns
// apart: no sqlc or PostgreSQL identifier may leak into the public contract.
func TestContractHasNoInternalTypeNames(t *testing.T) {
	raw := string(generatedContract(t))
	for _, forbidden := range []string{
		"postgres.", "sqlc.", "pgx", "GetResolutionRow", "UpsertPrimitive",
		"REUSERY_DATABASE_URL", "REUSERY_OPENAI_API_KEY", "REUSERY_GITHUB_TOKEN",
		"sk-", "ghp_", "github.com/matthewjameswatkins1978-cyber/reusery.dev/internal",
		"go-chi", "gin-gonic", "labstack/echo", "gofiber", "gorilla/mux",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("contract contains %q", forbidden)
		}
	}
}

// TestCheckedInContractMatchesGenerator is the OpenAPI drift gate: CI fails if
// Go API types or routes change without regenerating openapi/reusery-v1.json.
func TestCheckedInContractMatchesGenerator(t *testing.T) {
	path := filepath.Join("..", "..", "openapi", "reusery-v1.json")
	checked, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in contract: %v", err)
	}
	generated := generatedContract(t)
	if !bytes.Equal(checked, generated) {
		t.Fatalf("%s is out of date; regenerate with: go run ./cmd/openapi -write %s", path, path)
	}
}

func toStrings(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

func containsAll(values []string, want ...string) bool {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	for _, value := range want {
		if !set[value] {
			return false
		}
	}
	return true
}
