//go:build integration

package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/model"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/outcome"
	"github.com/matthewjameswatkins1978-cyber/reusery.dev/internal/resolver"
)

// postgresImage is pinned deliberately; do not move to a beta major.
const postgresImage = "postgres:18.6-alpine"

// newTestPool starts a real PostgreSQL container and returns an open pool. The
// database is empty and unmigrated, so callers control migration.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("reusery"),
		tcpostgres.WithUsername("reusery"),
		tcpostgres.WithPassword("reusery"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	connString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := Open(ctx, connString)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newMigratedStore starts a container, migrates it from zero and returns a store.
func newMigratedStore(t *testing.T) *Store {
	t.Helper()

	pool := newTestPool(t)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(pool)
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()

	var exists bool
	err := pool.QueryRow(context.Background(),
		"SELECT to_regclass($1) IS NOT NULL", "public."+name).Scan(&exists)
	if err != nil {
		t.Fatalf("table exists query for %q: %v", name, err)
	}
	return exists
}

// columnExists reports whether table carries a column, for migration lifecycle
// assertions that must distinguish 00001 from 00002.
func columnExists(t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()

	var exists bool
	err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("column exists query for %s.%s: %v", table, column, err)
	}
	return exists
}

// migrateUpTo applies migrations through the given version only, so the
// lifecycle test can observe the schema between 00001 and 00002.
func migrateUpTo(t *testing.T, pool *pgxpool.Pool, version int64) {
	t.Helper()

	provider, db, err := newProvider(pool)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := provider.UpTo(context.Background(), version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
}

// sameStrings compares ordered string slices, treating nil and empty as equal.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertResolutionEqual compares domain resolutions by meaning: ordered slices,
// nil/empty equivalence and instant equality for timestamps.
func assertResolutionEqual(t *testing.T, got, want model.Resolution) {
	t.Helper()

	if got.PrimitiveID != want.PrimitiveID || got.ContractID != want.ContractID ||
		got.Outcome != want.Outcome || got.SpecimenID != want.SpecimenID {
		t.Errorf("resolution identity = %#v, want %#v", got, want)
	}
	if !sameStrings(got.Reasons, want.Reasons) {
		t.Errorf("reasons = %v, want %v", got.Reasons, want.Reasons)
	}
	if !sameStrings(got.Unknowns, want.Unknowns) {
		t.Errorf("unknowns = %v, want %v", got.Unknowns, want.Unknowns)
	}
	if !sameStrings(got.EvidenceIDs, want.EvidenceIDs) {
		t.Errorf("evidence IDs = %v, want %v", got.EvidenceIDs, want.EvidenceIDs)
	}
	if got.PolicyID != want.PolicyID {
		t.Errorf("policy ID = %q, want %q", got.PolicyID, want.PolicyID)
	}
	if len(got.Rejected) != len(want.Rejected) {
		t.Fatalf("got %d rejections, want %d", len(got.Rejected), len(want.Rejected))
	}
	for i := range want.Rejected {
		if got.Rejected[i].SpecimenID != want.Rejected[i].SpecimenID ||
			!sameStrings(got.Rejected[i].Reasons, want.Rejected[i].Reasons) {
			t.Errorf("rejected[%d] = %#v, want %#v", i, got.Rejected[i], want.Rejected[i])
		}
	}
	if !got.ResolvedAt.Equal(want.ResolvedAt) {
		t.Errorf("resolvedAt = %v, want %v", got.ResolvedAt, want.ResolvedAt)
	}
}

func fixturePrimitive() model.Primitive {
	return model.Primitive{
		ID:          "process/bounded-subprocess",
		Name:        "Bounded subprocess execution",
		Description: "Execute a child process while bounding captured output and lifetime.",
		Tags:        []string{"process", "subprocess", "cancellation", "bounded-output"},
		ContractID:  "process/bounded-subprocess/v1",
	}
}

func fixtureContract() model.Contract {
	return model.Contract{
		ID:          "process/bounded-subprocess/v1",
		PrimitiveID: "process/bounded-subprocess",
		Version:     "1",
		Summary:     "Safely execute a subprocess without unbounded output or lifetime.",
		Requirements: []model.Requirement{
			{ID: "starts-requested-program", Kind: "behavior", Required: true, Description: "Starts the requested executable with the supplied arguments."},
			{ID: "preserves-exit-status", Kind: "behavior", Required: true, Description: "Reports successful and unsuccessful process termination without conflating non-zero exit with launch failure."},
			{ID: "drains-stdout-stderr-concurrently", Kind: "invariant", Required: true, Description: "Drains stdout and stderr without a read-order deadlock when either pipe fills."},
			{ID: "bounds-stdout", Kind: "resource", Required: true, Description: "Captured stdout cannot grow beyond the configured bound."},
			{ID: "bounds-stderr", Kind: "resource", Required: true, Description: "Captured stderr cannot grow beyond the configured bound."},
			{ID: "reports-truncation", Kind: "behavior", Required: true, Description: "Reports when captured stdout or stderr was truncated by a configured bound."},
			{ID: "supports-timeout", Kind: "lifecycle", Required: true, Description: "A configured deadline terminates or initiates termination of the running process."},
			{ID: "supports-cancellation", Kind: "lifecycle", Required: true, Description: "Caller cancellation terminates or initiates termination of the running process."},
			{ID: "process-tree-semantics-explicit", Kind: "platform", Required: true, Description: "Process-tree termination behaviour is explicit per supported platform rather than implied."},
			{ID: "no-orphaned-pipe-readers", Kind: "lifecycle", Required: true, Description: "Completion does not leave internal pipe-reading workers blocked indefinitely."},
			{ID: "result-distinguishes-failure-kinds", Kind: "error-model", Required: true, Description: "Result distinguishes launch failure, process exit, timeout, cancellation, and internal runner failure."},
		},
	}
}

func fixtureSpecimen() model.Specimen {
	return model.Specimen{
		ID:          "spec-os-exec",
		PrimitiveID: "process/bounded-subprocess",
		Name:        "os/exec bounded runner",
		Source: model.SourceRef{
			URL:      "https://example.com/os-exec-runner",
			Revision: "abc123",
			Path:     "internal/runner/runner.go",
			License:  "MIT",
		},
		ReuseMode: []model.ReuseMode{model.ReuseCopy, model.ReuseDependency},
	}
}

// A. Migration lifecycle: zero -> 00001 -> 00002 -> down -> up.
//
// Packet 7 adds resolutions.policy_id. The lifecycle deliberately observes the
// schema between the two migrations, because the whole point of 00002 is that
// a resolution written by a Packet 1-6 binary keeps loading afterwards with an
// empty PolicyID.
func TestIntegrationMigrationLifecycle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if tableExists(t, pool, "primitives") {
		t.Fatal("primitives should not exist before migration")
	}

	// zero -> 00001
	migrateUpTo(t, pool, 1)
	if !tableExists(t, pool, "primitives") {
		t.Fatal("primitives missing after 00001")
	}
	if columnExists(t, pool, "resolutions", "policy_id") {
		t.Fatal("policy_id must not exist before 00002")
	}
	if tableExists(t, pool, "resolution_feedback") {
		t.Fatal("resolution_feedback must not exist before 00003")
	}
	oldID := insertPrePolicyResolution(t, pool)

	// 00001 -> 00002
	migrateUpTo(t, pool, 2)
	if !columnExists(t, pool, "resolutions", "policy_id") {
		t.Fatal("policy_id missing after 00002")
	}
	if tableExists(t, pool, "resolution_feedback") {
		t.Fatal("resolution_feedback must not exist before 00003")
	}

	store := NewStore(pool)
	old, err := store.GetResolution(ctx, oldID)
	if err != nil {
		t.Fatalf("load pre-00002 resolution: %v", err)
	}
	if old.PolicyID != "" {
		t.Errorf("pre-00002 resolution PolicyID = %q, want empty", old.PolicyID)
	}

	// 00002 -> 00003
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate up to 00003: %v", err)
	}
	if !tableExists(t, pool, "resolution_feedback") {
		t.Fatal("resolution_feedback missing after 00003")
	}
	feedbackID := insertFeedbackRow(t, pool, oldID)

	// down: 00003 rolls back its own table and leaves 00001 and 00002 intact.
	if err := MigrateDown(ctx, pool); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if tableExists(t, pool, "resolution_feedback") {
		t.Fatal("resolution_feedback still present after migrate down")
	}
	if !columnExists(t, pool, "resolutions", "policy_id") {
		t.Fatal("policy_id must survive the 00003 rollback")
	}
	if !tableExists(t, pool, "primitives") {
		t.Fatal("00001 must survive the 00003 rollback")
	}
	if !tableExists(t, pool, "resolutions") {
		t.Fatal("resolutions must survive the 00003 rollback")
	}

	// up again
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	if !tableExists(t, pool, "primitives") {
		t.Fatal("primitives missing after second migrate up")
	}
	if !columnExists(t, pool, "resolutions", "policy_id") {
		t.Fatal("policy_id missing after second migrate up")
	}
	if !tableExists(t, pool, "resolution_feedback") {
		t.Fatal("resolution_feedback missing after second migrate up")
	}
	if _, err := store.GetResolution(ctx, oldID); err != nil {
		t.Fatalf("pre-00002 resolution must survive down/up: %v", err)
	}
	// The feedback row lived in the rolled-back table, so it is gone with it:
	// append-only history is never silently resurrected by a migration.
	var feedbackCount int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM resolution_feedback WHERE resolution_id = $1", oldID).Scan(&feedbackCount); err != nil {
		t.Fatalf("count feedback: %v", err)
	}
	if feedbackCount != 0 {
		t.Errorf("feedback rows = %d, want 0 after the 00003 rollback", feedbackCount)
	}
	_ = feedbackID
}

// insertFeedbackRow writes one outcome event so the 00003 rollback has
// something real to drop.
func insertFeedbackRow(t *testing.T, pool *pgxpool.Pool, resolutionID int64) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO resolution_feedback (resolution_id, kind, note, recorded_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		resolutionID, "adopted", "recorded during the migration lifecycle test",
		time.Date(2026, time.September, 26, 10, 0, 0, 0, time.UTC),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert resolution feedback: %v", err)
	}
	return id
}

// insertPrePolicyResolution writes a resolution exactly as a Packet 1-6 binary
// would have: no policy_id column exists yet.
func insertPrePolicyResolution(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO resolutions (primitive_id, contract_id, outcome, reasons, resolved_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		"process/bounded-subprocess", "process/bounded-subprocess/v1", "build_locally",
		[]string{"no candidate option satisfied all required contract requirements"},
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert pre-policy resolution: %v", err)
	}
	return id
}

// A Packet 7 resolution round-trips its PolicyID, and one produced without a
// policy round-trips an empty PolicyID.
func TestIntegrationResolutionPolicyIDRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()
	resolvedAt := time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC)

	cases := map[string]string{
		"with policy":    "public-go-baseline/v1",
		"without policy": "",
	}
	for name, policyID := range cases {
		t.Run(name, func(t *testing.T) {
			want := model.Resolution{
				PrimitiveID: "process/bounded-subprocess",
				ContractID:  "process/bounded-subprocess/v1",
				Outcome:     model.OutcomeReference,
				SpecimenID:  "public/github/code/example/repo@abc123:runner.go",
				Reasons:     []string{"selected as reference-only engineering knowledge"},
				Unknowns:    []string{"public/github/code/example/repo@abc123: supports-timeout unknown"},
				EvidenceIDs: []string{"ev-1"},
				PolicyID:    policyID,
				ResolvedAt:  resolvedAt,
			}
			id, err := store.InsertResolution(ctx, want)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			got, err := store.GetResolution(ctx, id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			assertResolutionEqual(t, got, want)
		})
	}
}

// B. Primitive round-trip including tags and contract id.
func TestIntegrationPrimitiveRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	want := fixturePrimitive()
	if err := store.UpsertPrimitive(ctx, want); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetPrimitive(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("primitive = %#v, want %#v", got, want)
	}
}

// C. Contract + requirement ordering across a real reload.
func TestIntegrationContractOrdering(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	want := fixtureContract()
	if err := store.UpsertContract(ctx, want); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetContract(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Requirements) != len(want.Requirements) {
		t.Fatalf("got %d requirements, want %d", len(got.Requirements), len(want.Requirements))
	}
	for i, req := range want.Requirements {
		if got.Requirements[i].ID != req.ID {
			t.Errorf("requirements[%d] = %q, want %q", i, got.Requirements[i].ID, req.ID)
		}
	}
	if !reflect.DeepEqual(got.Requirements, want.Requirements) {
		t.Errorf("requirements did not round-trip:\n got %#v\nwant %#v", got.Requirements, want.Requirements)
	}
}

// Upsert must replace requirements, not accumulate them.
func TestIntegrationContractUpsertReplacesRequirements(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	contract := fixtureContract()
	if err := store.UpsertContract(ctx, contract); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	contract.Requirements = contract.Requirements[:3]
	if err := store.UpsertContract(ctx, contract); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := store.GetContract(ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Requirements) != 3 {
		t.Fatalf("got %d requirements after replace, want 3", len(got.Requirements))
	}
}

// D. Specimen round-trip including SourceRef and reuse modes.
func TestIntegrationSpecimenRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	want := fixtureSpecimen()
	if err := store.UpsertSpecimen(ctx, want); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetSpecimen(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("specimen = %#v, want %#v", got, want)
	}
}

// E. Evidence round-trip for all four results, including explicit UNKNOWN.
func TestIntegrationEvidenceRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	observedAt := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	want := []model.Evidence{
		{
			ID: "ev-pass", SubjectID: "spec-os-exec", Kind: "unit-test", Claim: "stdout bounded",
			Result:     model.EvidencePass,
			Source:     model.SourceRef{URL: "https://example.com/ci/1", Revision: "abc123", Path: "runner_test.go", License: "MIT"},
			ObservedAt: observedAt, AppliesTo: "bounds-stdout", Methodology: "go test", Artifact: "artifact://ci/1",
		},
		{
			ID: "ev-fail", SubjectID: "spec-os-exec", Kind: "contract-test", Claim: "timeout not enforced",
			Result: model.EvidenceFail, ObservedAt: observedAt.Add(time.Minute), AppliesTo: "supports-timeout",
		},
		{
			ID: "ev-unknown", SubjectID: "spec-os-exec", Kind: "review", Claim: "windows tree semantics unverified",
			Result: model.EvidenceUnknown, ObservedAt: observedAt.Add(2 * time.Minute),
			AppliesTo: "process-tree-semantics-explicit",
		},
		{
			// Deliberately empty optional fields.
			ID: "ev-info", SubjectID: "spec-os-exec", Result: model.EvidenceInfo,
			ObservedAt: observedAt.Add(3 * time.Minute),
		},
	}

	for _, e := range want {
		if err := store.InsertEvidence(ctx, e); err != nil {
			t.Fatalf("insert %q: %v", e.ID, err)
		}
	}

	loaded, err := store.ListEvidenceBySubject(ctx, "spec-os-exec")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(loaded) != len(want) {
		t.Fatalf("got %d evidence rows, want %d", len(loaded), len(want))
	}

	byID := make(map[string]model.Evidence, len(loaded))
	for _, e := range loaded {
		byID[e.ID] = e
	}
	for _, expected := range want {
		got, ok := byID[expected.ID]
		if !ok {
			t.Fatalf("evidence %q missing", expected.ID)
		}
		if got.ID != expected.ID || got.SubjectID != expected.SubjectID || got.Kind != expected.Kind ||
			got.Claim != expected.Claim || got.Result != expected.Result || got.AppliesTo != expected.AppliesTo ||
			got.Methodology != expected.Methodology || got.Artifact != expected.Artifact {
			t.Errorf("evidence %q fields = %#v, want %#v", expected.ID, got, expected)
		}
		if !reflect.DeepEqual(got.Source, expected.Source) {
			t.Errorf("evidence %q source = %#v, want %#v", expected.ID, got.Source, expected.Source)
		}
		if !got.ObservedAt.Equal(expected.ObservedAt) {
			t.Errorf("evidence %q observedAt = %v, want %v", expected.ID, got.ObservedAt, expected.ObservedAt)
		}
	}
}

// A duplicate evidence ID must be rejected, not silently overwrite.
func TestIntegrationEvidenceAppendOnly(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	e := model.Evidence{
		ID: "ev-dupe", SubjectID: "spec-os-exec", Result: model.EvidencePass,
		ObservedAt: time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC),
	}
	if err := store.InsertEvidence(ctx, e); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := store.InsertEvidence(ctx, e); err == nil {
		t.Fatal("duplicate evidence insert succeeded, want error")
	}
}

// F. Resolution round-trip, including BUILD LOCALLY without a specimen.
func TestIntegrationResolutionRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	resolvedAt := time.Date(2026, time.September, 25, 13, 30, 0, 0, time.UTC)
	want := model.Resolution{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Outcome:     model.OutcomeBuildLocally,
		SpecimenID:  "",
		Reasons:     []string{"no candidate has verified process-tree semantics", "reviewed candidates are worse under constraints"},
		Rejected: []model.Rejection{
			{SpecimenID: "spec-a", Reasons: []string{"no bounding evidence", "no cancellation evidence"}},
			{SpecimenID: "spec-b", Reasons: []string{"process-tree termination unknown on Windows"}},
		},
		Unknowns:    []string{"macOS behaviour not verified"},
		EvidenceIDs: []string{"ev-1", "ev-2", "ev-3"},
		ResolvedAt:  resolvedAt,
	}

	id, err := store.InsertResolution(ctx, want)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetResolution(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertResolutionEqual(t, got, want)
}

// A resolution with a selected specimen keeps its specimen ID.
func TestIntegrationResolutionWithSpecimen(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	want := model.Resolution{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Outcome:     model.OutcomeAdapt,
		SpecimenID:  "spec-os-exec",
		Reasons:     []string{"satisfies required requirements with bounded adaptation"},
		ResolvedAt:  time.Date(2026, time.September, 25, 14, 0, 0, 0, time.UTC),
	}

	id, err := store.InsertResolution(ctx, want)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.GetResolution(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SpecimenID != want.SpecimenID {
		t.Errorf("specimen id = %q, want %q", got.SpecimenID, want.SpecimenID)
	}
	assertResolutionEqual(t, got, want)
}

// G. Transaction rollback for Contract + Requirements.
func TestIntegrationContractTransactionRollback(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	bad := fixtureContract()
	bad.ID = "process/broken/v1"
	// Duplicate requirement IDs violate PRIMARY KEY(contract_id, requirement_id)
	// after the contract row itself has been written.
	bad.Requirements = []model.Requirement{
		{ID: "dupe", Required: true},
		{ID: "dupe", Required: true},
	}

	if err := store.UpsertContract(ctx, bad); err == nil {
		t.Fatal("upsert with duplicate requirement IDs succeeded, want error")
	}

	if _, err := store.GetContract(ctx, bad.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("contract left behind after rollback: err = %v, want ErrNotFound", err)
	}
}

// G. Transaction rollback for Resolution + Rejections.
func TestIntegrationResolutionTransactionRollback(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	bad := model.Resolution{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Outcome:     model.OutcomeBuildLocally,
		Reasons:     []string{"should not survive"},
		// An empty rejection specimen ID violates the rejections check
		// constraint after the resolution row has been written.
		Rejected:   []model.Rejection{{SpecimenID: "", Reasons: []string{"invalid"}}},
		ResolvedAt: time.Date(2026, time.September, 25, 15, 0, 0, 0, time.UTC),
	}

	if _, err := store.InsertResolution(ctx, bad); err == nil {
		t.Fatal("insert with invalid rejection succeeded, want error")
	}

	count, err := store.CountResolutions(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("resolution persisted despite rollback: count = %d", count)
	}
}

// H. Database readiness reflects real connectivity.
func TestIntegrationReadiness(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ready := ReadyChecker(pool)

	if err := ready(ctx); err != nil {
		t.Fatalf("ready checker = %v, want nil with PostgreSQL available", err)
	}

	pool.Close()
	if err := ready(ctx); err == nil {
		t.Fatal("ready checker = nil after pool closed, want error")
	}
}

// I. Persisted values feed resolver.Evaluate unchanged.
func TestIntegrationEvaluatorCompatibility(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	contract := fixtureContract()
	specimen := fixtureSpecimen()
	if err := store.UpsertContract(ctx, contract); err != nil {
		t.Fatalf("upsert contract: %v", err)
	}
	if err := store.UpsertSpecimen(ctx, specimen); err != nil {
		t.Fatalf("upsert specimen: %v", err)
	}

	observedAt := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	evidence := []model.Evidence{
		{ID: "ev-1", SubjectID: specimen.ID, Result: model.EvidencePass, ObservedAt: observedAt, AppliesTo: "bounds-stdout"},
		{ID: "ev-2", SubjectID: specimen.ID, Result: model.EvidencePass, ObservedAt: observedAt, AppliesTo: "bounds-stderr"},
		{ID: "ev-3", SubjectID: specimen.ID, Result: model.EvidenceFail, ObservedAt: observedAt, AppliesTo: "supports-timeout"},
		{ID: "ev-4", SubjectID: specimen.ID, Result: model.EvidencePass, ObservedAt: observedAt, AppliesTo: "supports-cancellation"},
		{ID: "ev-5", SubjectID: specimen.ID, Result: model.EvidenceUnknown, ObservedAt: observedAt, AppliesTo: "process-tree-semantics-explicit"},
	}
	for _, e := range evidence {
		if err := store.InsertEvidence(ctx, e); err != nil {
			t.Fatalf("insert evidence %q: %v", e.ID, err)
		}
	}

	loadedContract, err := store.GetContract(ctx, contract.ID)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	loadedSpecimen, err := store.GetSpecimen(ctx, specimen.ID)
	if err != nil {
		t.Fatalf("reload specimen: %v", err)
	}
	loadedEvidence, err := store.ListEvidenceBySubject(ctx, specimen.ID)
	if err != nil {
		t.Fatalf("reload evidence: %v", err)
	}

	evaluation, err := resolver.Evaluate(loadedContract, loadedSpecimen, loadedEvidence)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	want := map[string]resolver.RequirementStatus{
		"bounds-stdout":                   resolver.RequirementSatisfied,
		"bounds-stderr":                   resolver.RequirementSatisfied,
		"supports-timeout":                resolver.RequirementFailed,
		"supports-cancellation":           resolver.RequirementSatisfied,
		"process-tree-semantics-explicit": resolver.RequirementUnknown,
		"starts-requested-program":        resolver.RequirementUnknown,
	}
	for _, req := range evaluation.Requirements {
		expected, checked := want[req.RequirementID]
		if !checked {
			continue
		}
		if req.Status != expected {
			t.Errorf("%s status = %q, want %q", req.RequirementID, req.Status, expected)
		}
	}
}

// Outcome feedback is append-only factual history: it round-trips in
// chronological order and never alters the Resolution it reports on.
func TestIntegrationOutcomeFeedbackRoundTrip(t *testing.T) {
	store := newMigratedStore(t)
	ctx := context.Background()

	resolutionID, err := store.InsertResolution(ctx, model.Resolution{
		PrimitiveID: "process/bounded-subprocess",
		ContractID:  "process/bounded-subprocess/v1",
		Outcome:     model.OutcomeDepend,
		SpecimenID:  "fixture/process/bounded-subprocess/complete-dependency",
		Reasons:     []string{"all required requirements satisfied"},
		PolicyID:    "public-go-baseline/v1",
		ResolvedAt:  time.Date(2026, time.September, 26, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("insert resolution: %v", err)
	}
	before, err := store.GetResolution(ctx, resolutionID)
	if err != nil {
		t.Fatalf("load resolution: %v", err)
	}

	for index, kind := range []outcome.Kind{outcome.KindAdopted, outcome.KindIntegrationSucceeded} {
		id, err := store.InsertFeedback(ctx, outcome.Feedback{
			ResolutionID: resolutionID,
			Kind:         kind,
			Note:         "actual result " + string(kind),
			RecordedAt:   time.Date(2026, time.September, 26, 10+index, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("insert feedback %d: %v", index, err)
		}
		if id == 0 {
			t.Error("inserted feedback has no identity")
		}
	}

	events, err := store.ListFeedback(ctx, resolutionID, 10)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Kind != outcome.KindAdopted || events[1].Kind != outcome.KindIntegrationSucceeded {
		t.Errorf("events out of order: %v", events)
	}
	if events[0].Note == "" {
		t.Error("note did not round-trip")
	}

	after, err := store.GetResolution(ctx, resolutionID)
	if err != nil {
		t.Fatalf("reload resolution: %v", err)
	}
	if after.Outcome != before.Outcome || after.PolicyID != before.PolicyID ||
		!after.ResolvedAt.Equal(before.ResolvedAt) {
		t.Errorf("reporting feedback changed the Resolution: %+v", after)
	}

	// A resolution that does not exist has no feedback.
	if events, err := store.ListFeedback(ctx, 999, 10); err != nil || len(events) != 0 {
		t.Errorf("unknown resolution: events=%v err=%v", events, err)
	}
}
