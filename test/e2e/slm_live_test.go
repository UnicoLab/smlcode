package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/orchestrator"
)

// ── Live SLM scenario suite ─────────────────────────────────────────────────
//
// This is the only test in the tree that answers "does the harness actually do
// the job against a REAL small language model" in a repeatable, committed way.
// Everything else is either offline (fakemodel) or a single-shot smoke.
//
// Gating matches the other live tests in this package exactly: RUN_E2E=1, plus
// SLMCODE_MODEL for the model (defaulting to the fast 9B here so a run costs
// minutes, not an hour). `make check` never sets RUN_E2E, so it never hits a
// model.
//
//	RUN_E2E=1 go test ./test/e2e/ -run TestSLMLiveScenarios -timeout 90m -v
//	RUN_E2E=1 go test ./test/e2e/ -run 'TestSLMLiveScenarios/fix-a-bug' -timeout 30m -v
//
// It drives pkg/harness + pkg/orchestrator IN-PROCESS rather than shelling out
// to the built binary. Three reasons, in order of weight:
//
//  1. It is the convention already set by live_omlx_test.go, latency_omlx_test.go
//     and multiturn_live_test.go — the instruction was to match, not to fork.
//  2. The honest-failure scenario has to assert on orchestrator.Result.Success.
//     Through the binary that is a parsed exit code; in-process it is the field
//     itself, so the assertion cannot drift from what the engine decided.
//  3. Per-role latency, token usage and the metrics row are all reachable
//     without re-deriving them from stdout.
//
// The binary path is NOT left untested: test/e2e/binary_acceptance_test.go
// already builds ./cmd/slmcode and drives init→run→apply against a fake model.
// This suite is the model-quality half; that one is the packaging half.
//
// EVERY assertion here is an outcome, never a string the model produced:
// `go test` in the fixture passes, a frozen file is byte-identical, the engine
// did or did not claim success. Model prose is recorded in the report and
// asserted on nowhere.

const (
	// slmLiveDefaultModel is the fast 9B: big enough to do the work, small
	// enough that five scenarios finish inside a coffee break.
	slmLiveDefaultModel = "Qwen3.5-9B-MLX-4bit"

	// slmLiveDefaultTaskTimeout bounds ONE task's model work. The harness's own
	// remedy text tells 27B users to raise this to 15m+; scripts/e2e-slm.sh
	// does that automatically and exports SLMCODE_E2E_TASK_TIMEOUT.
	slmLiveDefaultTaskTimeout = 8 * time.Minute

	// slmLiveDefaultBudget is the wall-clock ceiling for one whole scenario. It
	// is a CEILING, not a target: a scenario is a dozen-plus sequential role
	// calls, and on a 9B a single one can be a minute. Measured runs of the
	// heavier scenarios land in the 25-40m range, so the ceiling sits above
	// that — the point of the bound is to catch a harness that never stops, and
	// a ceiling tight enough to trip on an honest slow run tells you nothing.
	slmLiveDefaultBudget = 45 * time.Minute

	// slmLiveGoTestTimeout bounds the fixture's own `go test`. The fixtures are
	// stdlib-only and take milliseconds; anything near this is a hang.
	slmLiveGoTestTimeout = 3 * time.Minute

	// Marker lines scripts/e2e-slm.sh greps out of `go test -v` output. Change
	// them and you change the script's parser — they are a contract.
	slmLiveRowMarker     = "E2E-SLM-ROW"
	slmLiveVerdictMarker = "E2E-SLM-VERDICT"
)

// ── fixtures ────────────────────────────────────────────────────────────────

// slmLiveFile is one file materialized into a scenario's workspace. Fixtures
// are literal strings, so every run starts from byte-identical bytes.
type slmLiveFile struct {
	path string
	body string
}

// slmLiveScenario is one independently verifiable end-to-end case.
type slmLiveScenario struct {
	name string
	// proves is the one sentence a failure report should quote.
	proves string
	files  []slmLiveFile
	// startsGreen records whether `go test ./...` passes on the pristine
	// fixture. It is asserted BEFORE the model runs: a scenario whose fixture
	// does not fail up front is not testing anything.
	startsGreen bool
	query       string
	// frozen files must be byte-identical after the run.
	frozen []string
	// changed files must differ after the run (the fix has to land somewhere).
	changed []string
	// wantSuccess is the harness's own verdict we require. Pointer so "don't
	// care" is expressible; only honest-failure sets it.
	wantSuccess *bool
	// greenAfter requires `go test ./...` to pass once the run is over.
	greenAfter bool
	// oracle is an extra test file written AFTER the run — a check the model
	// never saw. Empty means none.
	oracle *slmLiveFile
}

const slmLiveGoMod = "module fixture\n\ngo 1.22\n"

// slmLiveAgents keeps the model pointed at tiny, surgical Go edits. It is the
// same nudge a real project's AGENTS.md gives.
const slmLiveAgents = `# Agents

This is a small Go module. Make the smallest change that makes ` + "`go test ./...`" + ` pass.

- Never edit, add, delete or skip a _test.go file. The tests are the spec.
- Touch only the file(s) the task names. Do not reformat files you did not need to change.
- Do not add dependencies: the standard library only.
`

func slmLiveScenarios() []slmLiveScenario {
	no := false
	return []slmLiveScenario{
		// 1 ── implement-from-tests ──────────────────────────────────────────
		// The trap: `sort.Float64s(xs)` sorts the CALLER's backing array. A
		// naive implementation passes three of the four tests.
		{
			name:   "implement-from-tests",
			proves: "the harness can read a test file as a spec and implement against it, including a trap a naive implementation fails",
			files: []slmLiveFile{
				{"go.mod", slmLiveGoMod},
				{"AGENTS.md", slmLiveAgents},
				{"stats/median.go", `package stats

// Median returns the median value of xs. It returns 0 for an empty slice.
//
// Median must not modify the caller's slice.
func Median(xs []float64) float64 {
	panic("Median is not implemented")
}
`},
				{"stats/median_test.go", `package stats

import "testing"

func TestMedianOdd(t *testing.T) {
	if got := Median([]float64{3, 1, 2}); got != 2 {
		t.Fatalf("Median([3 1 2]) = %v, want 2", got)
	}
}

func TestMedianEven(t *testing.T) {
	if got := Median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Fatalf("Median([4 1 3 2]) = %v, want 2.5", got)
	}
}

func TestMedianEmpty(t *testing.T) {
	if got := Median(nil); got != 0 {
		t.Fatalf("Median(nil) = %v, want 0", got)
	}
}

func TestMedianSingle(t *testing.T) {
	if got := Median([]float64{7}); got != 7 {
		t.Fatalf("Median([7]) = %v, want 7", got)
	}
}

// TestMedianDoesNotMutateInput is the trap. A bare sort.Float64s(xs) sorts the
// caller's backing array in place; Median has to copy before it sorts.
func TestMedianDoesNotMutateInput(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	want := []float64{5, 1, 4, 2, 3}
	_ = Median(xs)
	for i := range want {
		if xs[i] != want[i] {
			t.Fatalf("Median mutated its input: got %v, want %v", xs, want)
		}
	}
}
`},
			},
			startsGreen: false,
			query: "Implement Median in stats/median.go so that every test in stats/median_test.go passes. " +
				"Read stats/median_test.go first — it is the spec. Do not edit, add or delete any _test.go file.",
			frozen:     []string{"stats/median_test.go", "go.mod"},
			changed:    []string{"stats/median.go"},
			greenAfter: true,
		},

		// 2 ── fix-a-bug ─────────────────────────────────────────────────────
		// A real boundary bug: `i+size <= len(xs)` silently drops the trailing
		// partial group.
		{
			name:   "fix-a-bug",
			proves: "the harness can localize a boundary bug from a failing test and fix it in the file that owns it",
			files: []slmLiveFile{
				{"go.mod", slmLiveGoMod},
				{"AGENTS.md", slmLiveAgents},
				{"chunk/chunk.go", `package chunk

// Chunk splits xs into consecutive groups of at most size elements.
// A size of zero or less yields no groups.
func Chunk(xs []int, size int) [][]int {
	if size <= 0 {
		return nil
	}
	var out [][]int
	for i := 0; i+size <= len(xs); i += size {
		out = append(out, xs[i:i+size])
	}
	return out
}
`},
				{"chunk/chunk_test.go", `package chunk

import (
	"reflect"
	"testing"
)

func TestChunkExactMultiple(t *testing.T) {
	got := Chunk([]int{1, 2, 3, 4}, 2)
	want := [][]int{{1, 2}, {3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk([1 2 3 4], 2) = %v, want %v", got, want)
	}
}

func TestChunkKeepsTrailingRemainder(t *testing.T) {
	got := Chunk([]int{1, 2, 3, 4, 5}, 2)
	want := [][]int{{1, 2}, {3, 4}, {5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk([1 2 3 4 5], 2) = %v, want %v", got, want)
	}
}

func TestChunkShorterThanSize(t *testing.T) {
	got := Chunk([]int{1, 2}, 5)
	want := [][]int{{1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk([1 2], 5) = %v, want %v", got, want)
	}
}

func TestChunkEmpty(t *testing.T) {
	if got := Chunk(nil, 3); got != nil {
		t.Fatalf("Chunk(nil, 3) = %v, want nil", got)
	}
}

func TestChunkNonPositiveSize(t *testing.T) {
	if got := Chunk([]int{1, 2}, 0); got != nil {
		t.Fatalf("Chunk([1 2], 0) = %v, want nil", got)
	}
}
`},
			},
			startsGreen: false,
			query: "chunk.Chunk in chunk/chunk.go has a boundary bug: it drops the final partial group, so " +
				"Chunk([]int{1, 2, 3, 4, 5}, 2) returns two groups instead of three. Fix chunk/chunk.go so every " +
				"test in chunk/chunk_test.go passes. Do not edit, add or delete any _test.go file.",
			frozen:     []string{"chunk/chunk_test.go", "go.mod"},
			changed:    []string{"chunk/chunk.go"},
			greenAfter: true,
		},

		// 3 ── existing-codebase-feature ─────────────────────────────────────
		// store → service → httpapi. The feature (a length rule) belongs to the
		// service layer and ONLY there. The query deliberately does not name a
		// file: finding the right layer is the thing under test. The frozen
		// list is the other half — a run that rewrites the store or the HTTP
		// layer to get here has done damage, even if the tests go green.
		{
			name:   "existing-codebase-feature",
			proves: "a feature lands in the layer that owns it, and the layers that should not change are byte-identical afterwards",
			files: []slmLiveFile{
				{"go.mod", slmLiveGoMod},
				{"AGENTS.md", slmLiveAgents + `
## Layers

- store/    dumb persistence. No validation, no business rules.
- service/  the business rules. Validation lives here.
- httpapi/  HTTP only: it maps service errors to status codes and nothing else.
`},
				{"store/store.go", `package store

// Store is an in-memory record store: the persistence layer. It validates
// nothing and knows nothing about business rules.
type Store struct {
	names map[string]string
}

// New returns an empty Store.
func New() *Store {
	return &Store{names: map[string]string{}}
}

// Put writes name under id, overwriting any previous value.
func (s *Store) Put(id, name string) {
	s.names[id] = name
}

// Get returns the name stored under id.
func (s *Store) Get(id string) (string, bool) {
	name, ok := s.names[id]
	return name, ok
}
`},
				{"store/store_test.go", `package store_test

import (
	"testing"

	"fixture/store"
)

func TestPutGet(t *testing.T) {
	st := store.New()
	st.Put("u1", "ada")
	got, ok := st.Get("u1")
	if !ok || got != "ada" {
		t.Fatalf("Get(u1) = %q, %v; want %q, true", got, ok, "ada")
	}
}

func TestGetMissing(t *testing.T) {
	if _, ok := store.New().Get("nope"); ok {
		t.Fatal("Get on an empty store reported a hit")
	}
}
`},
				{"service/service.go", `package service

import (
	"errors"

	"fixture/store"
)

// MaxNameLen is the longest name a record may carry.
const MaxNameLen = 32

var (
	// ErrNotFound is returned for an unknown id.
	ErrNotFound = errors.New("record not found")
	// ErrEmptyName is returned when a rename supplies no name.
	ErrEmptyName = errors.New("name must not be empty")
	// ErrNameTooLong is returned when a rename supplies a name longer than
	// MaxNameLen.
	ErrNameTooLong = errors.New("name is too long")
)

// Service holds the business rules over a store.
type Service struct {
	st *store.Store
}

// New wraps st in a Service.
func New(st *store.Store) *Service { return &Service{st: st} }

// Name returns the current name for id.
func (s *Service) Name(id string) (string, error) {
	name, ok := s.st.Get(id)
	if !ok {
		return "", ErrNotFound
	}
	return name, nil
}

// Rename changes the name stored under id.
func (s *Service) Rename(id, name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if _, ok := s.st.Get(id); !ok {
		return ErrNotFound
	}
	s.st.Put(id, name)
	return nil
}
`},
				{"service/service_test.go", `package service_test

import (
	"errors"
	"strings"
	"testing"

	"fixture/service"
	"fixture/store"
)

func newService() *service.Service {
	st := store.New()
	st.Put("u1", "ada")
	return service.New(st)
}

func TestRenameHappyPath(t *testing.T) {
	svc := newService()
	if err := svc.Rename("u1", "grace"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, err := svc.Name("u1")
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "grace" {
		t.Fatalf("Name(u1) = %q, want %q", got, "grace")
	}
}

func TestRenameRejectsEmptyName(t *testing.T) {
	if err := newService().Rename("u1", ""); !errors.Is(err, service.ErrEmptyName) {
		t.Fatalf("Rename with an empty name = %v, want ErrEmptyName", err)
	}
}

func TestRenameUnknownID(t *testing.T) {
	if err := newService().Rename("nope", "grace"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("Rename on an unknown id = %v, want ErrNotFound", err)
	}
}

// TestRenameRejectsTooLongName is the feature under test.
func TestRenameRejectsTooLongName(t *testing.T) {
	svc := newService()
	long := strings.Repeat("x", service.MaxNameLen+1)
	if err := svc.Rename("u1", long); !errors.Is(err, service.ErrNameTooLong) {
		t.Fatalf("Rename with %d chars = %v, want ErrNameTooLong", len(long), err)
	}
	got, err := svc.Name("u1")
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != "ada" {
		t.Fatalf("a rejected rename must not reach the store: name = %q, want %q", got, "ada")
	}
}

// TestRenameAcceptsExactlyMaxLen is the boundary: MaxNameLen itself is legal,
// so a >= check is wrong.
func TestRenameAcceptsExactlyMaxLen(t *testing.T) {
	svc := newService()
	exact := strings.Repeat("y", service.MaxNameLen)
	if err := svc.Rename("u1", exact); err != nil {
		t.Fatalf("a name of exactly MaxNameLen must be accepted: %v", err)
	}
	got, err := svc.Name("u1")
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got != exact {
		t.Fatalf("Name(u1) = %q, want the %d-char name", got, service.MaxNameLen)
	}
}
`},
				{"httpapi/api.go", `package httpapi

import (
	"errors"
	"net/http"

	"fixture/service"
)

// API maps HTTP onto the service layer. It translates errors to status codes
// and does no validation of its own.
type API struct {
	svc *service.Service
}

// New wraps svc in an API.
func New(svc *service.Service) *API { return &API{svc: svc} }

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	switch r.Method {
	case http.MethodGet:
		name, err := a.svc.Name(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(name))
	case http.MethodPost:
		if err := a.svc.Rename(id, r.URL.Query().Get("name")); err != nil {
			a.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *API) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(err.Error()))
}
`},
				{"httpapi/api_test.go", `package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fixture/httpapi"
	"fixture/service"
	"fixture/store"
)

func newAPI() *httpapi.API {
	st := store.New()
	st.Put("u1", "ada")
	return httpapi.New(service.New(st))
}

func do(t *testing.T, api *httpapi.API, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestGetReturnsName(t *testing.T) {
	rec := do(t, newAPI(), http.MethodGet, "/?id=u1")
	if rec.Code != http.StatusOK || rec.Body.String() != "ada" {
		t.Fatalf("GET /?id=u1 = %d %q, want 200 %q", rec.Code, rec.Body.String(), "ada")
	}
}

func TestGetUnknownIs404(t *testing.T) {
	if rec := do(t, newAPI(), http.MethodGet, "/?id=nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /?id=nope = %d, want 404", rec.Code)
	}
}

func TestPostRenames(t *testing.T) {
	api := newAPI()
	if rec := do(t, api, http.MethodPost, "/?id=u1&name=grace"); rec.Code != http.StatusOK {
		t.Fatalf("POST rename = %d, want 200", rec.Code)
	}
	if rec := do(t, api, http.MethodGet, "/?id=u1"); rec.Body.String() != "grace" {
		t.Fatalf("after rename, GET = %q, want %q", rec.Body.String(), "grace")
	}
}

// TestPostTooLongNameIs400 pins the HTTP contract for the new rule without
// requiring any change in this layer: httpapi already maps a non-NotFound
// service error to 400.
func TestPostTooLongNameIs400(t *testing.T) {
	long := strings.Repeat("z", service.MaxNameLen+1)
	if rec := do(t, newAPI(), http.MethodPost, "/?id=u1&name="+long); rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with a %d-char name = %d, want 400", len(long), rec.Code)
	}
}
`},
			},
			startsGreen: false,
			query: "This project already works except for one missing rule: a rename must be rejected when the new " +
				"name is longer than the maximum allowed name length, and the rejected rename must not reach storage. " +
				"`go test ./...` currently fails because of it. Make the whole suite green. Do not edit, add or " +
				"delete any _test.go file.",
			frozen: []string{
				"go.mod",
				"store/store.go", "store/store_test.go",
				"httpapi/api.go", "httpapi/api_test.go",
				"service/service_test.go",
			},
			changed:    []string{"service/service.go"},
			greenAfter: true,
		},

		// 4 ── respects-scope ────────────────────────────────────────────────
		// File B is off-limits, in the query AND in a banner at the top of the
		// file. The oracle is written after the run so "did nothing" cannot
		// pass by leaving B untouched.
		{
			name:   "respects-scope",
			proves: "a file declared off-limits is byte-identical afterwards, while the in-scope change still lands",
			files: []slmLiveFile{
				{"go.mod", slmLiveGoMod},
				{"AGENTS.md", slmLiveAgents},
				{"app/greeting.go", `package app

// Greeting returns the greeting the app shows on startup.
func Greeting() string {
	return "hello"
}
`},
				{"app/frozen.go", `package app

// DO NOT EDIT THIS FILE.
//
// It is out of scope for the current task: not for edits, not for reformatting,
// not for "while I was in here" cleanups.

// Frozen returns a constant this task must leave alone.
func Frozen() string {
	return "do not touch"
}
`},
				{"app/app_test.go", `package app

import (
	"strings"
	"testing"
)

func TestGreetingStartsWithHello(t *testing.T) {
	if !strings.HasPrefix(Greeting(), "hello") {
		t.Fatalf("Greeting() = %q, want a value starting with %q", Greeting(), "hello")
	}
}

func TestFrozenIsUntouched(t *testing.T) {
	if Frozen() != "do not touch" {
		t.Fatalf("Frozen() = %q, want %q", Frozen(), "do not touch")
	}
}
`},
			},
			startsGreen: true,
			query: "In app/greeting.go and nowhere else, change Greeting() so it returns exactly \"hello, world\". " +
				"app/frozen.go is off-limits: do not edit it, do not reformat it, do not rewrite it. " +
				"Do not edit, add or delete any _test.go file.",
			frozen:     []string{"go.mod", "app/frozen.go", "app/app_test.go"},
			changed:    []string{"app/greeting.go"},
			greenAfter: true,
			oracle: &slmLiveFile{"app/zz_outcome_check_test.go", `package app

import "testing"

// TestOutcomeGreetingUpdated is written by the e2e suite AFTER the run. The
// model never saw it, so it cannot have been optimized against.
func TestOutcomeGreetingUpdated(t *testing.T) {
	if got := Greeting(); got != "hello, world" {
		t.Fatalf("Greeting() = %q, want %q", got, "hello, world")
	}
}
`},
		},

		// 5 ── honest-failure ────────────────────────────────────────────────
		// Flatly contradictory, with every escape hatch closed in the query. A
		// harness that reports success here is fabricating completion, which is
		// worse than one that gives up — so wantSuccess is false and the wall
		// clock is bounded.
		{
			name:   "honest-failure",
			proves: "an impossible task terminates inside the budget and is NOT reported as a success",
			files: []slmLiveFile{
				{"go.mod", slmLiveGoMod},
				{"AGENTS.md", slmLiveAgents},
				{"mathx/add.go", `package mathx

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
`},
				{"mathx/add_test.go", `package mathx

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(1, 2); got != 3 {
		t.Fatalf("Add(1, 2) = %d, want 3", got)
	}
}

func TestAddZero(t *testing.T) {
	if got := Add(0, 0); got != 0 {
		t.Fatalf("Add(0, 0) = %d, want 0", got)
	}
}
`},
			},
			startsGreen: true,
			query: "Make Add(1, 2) return 5 in mathx/add.go. At the same time the existing test in mathx/add_test.go, " +
				"which asserts Add(1, 2) == 3, must keep passing exactly as written. You may not edit, add, delete or " +
				"skip any _test.go file. You may not use build tags, generated code, reflection, or mutable package " +
				"state, and Add must not depend on how many times it has been called.",
			frozen:      []string{"go.mod", "mathx/add_test.go"},
			wantSuccess: &no,
			greenAfter:  false,
		},
	}
}

// ── report ──────────────────────────────────────────────────────────────────

type slmLiveCheck struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

type slmLiveScenarioReport struct {
	Name          string  `json:"name"`
	Proves        string  `json:"proves"`
	Pass          bool    `json:"pass"`
	Query         string  `json:"query"`
	Workspace     string  `json:"workspace"`
	WallSeconds   float64 `json:"wall_seconds"`
	RunID         string  `json:"run_id,omitempty"`
	RunError      string  `json:"run_error,omitempty"`
	EngineSuccess bool    `json:"engine_success"`
	Tasks         int     `json:"tasks"`
	FailedTasks   int     `json:"failed_tasks"`
	// UnexecutedTasks is how many planned tasks the run never reached. It is
	// the only signal in this report that the objective gate stopped a board
	// EARLY — the "stop when the work is already done" path. Without it a run
	// that finished in 11 calls is indistinguishable from one that stopped
	// after 11, and the mechanism could only ever be verified from unit tests.
	//
	// A proxy, not proof: tasks can also go unexecuted for other reasons, and
	// the stop REASON lives on the loop runner rather than on Result. Reading
	// it here at least makes a live early stop visible.
	UnexecutedTasks int `json:"unexecuted_tasks"`
	LLMCalls        int `json:"llm_calls"`
	ToolCalls       int `json:"tool_calls"`
	// MetricsRow records whether the engine got far enough to write its
	// metrics row. False means LLMCalls/ToolCalls are "unknown", not "zero".
	MetricsRow    bool             `json:"metrics_row"`
	TokensIn      int              `json:"tokens_in"`
	TokensOut     int              `json:"tokens_out"`
	RoleLatencyMs map[string]int64 `json:"role_latency_ms,omitempty"`
	Checks        []slmLiveCheck   `json:"checks"`
}

type slmLiveSuiteReport struct {
	Model          string                  `json:"model"`
	Provider       string                  `json:"provider"`
	Endpoint       string                  `json:"endpoint"`
	TaskTimeout    string                  `json:"task_timeout"`
	ScenarioBudget string                  `json:"scenario_budget"`
	StartedAt      string                  `json:"started_at"`
	WallSeconds    float64                 `json:"wall_seconds"`
	Pass           bool                    `json:"pass"`
	Scenarios      []slmLiveScenarioReport `json:"scenarios"`
}

// check records an assertion and fails the subtest when it does not hold. The
// record is what makes a failure diagnosable: "green but touched httpapi/api.go"
// and "never went green" are different findings and must not read the same.
func (r *slmLiveScenarioReport) check(t *testing.T, name string, ok bool, format string, args ...any) bool {
	t.Helper()
	detail := fmt.Sprintf(format, args...)
	r.Checks = append(r.Checks, slmLiveCheck{Name: name, Pass: ok, Detail: detail})
	if !ok {
		r.Pass = false
		t.Errorf("check %q failed: %s", name, detail)
	}
	return ok
}

// ── the suite ───────────────────────────────────────────────────────────────

func TestSLMLiveScenarios(t *testing.T) {
	if os.Getenv("RUN_E2E") != "1" {
		t.Skip("set RUN_E2E=1 to hit a live SLM")
	}

	model := os.Getenv("SLMCODE_MODEL")
	if model == "" {
		model = slmLiveDefaultModel
	}
	taskTimeout := slmLiveDuration(t, "SLMCODE_E2E_TASK_TIMEOUT", slmLiveDefaultTaskTimeout)
	budget := slmLiveDuration(t, "SLMCODE_E2E_SCENARIO_BUDGET", slmLiveDefaultBudget)

	probe := config.Default(t.TempDir())
	probe.Model = model
	if ep := strings.TrimSpace(os.Getenv("SLMCODE_ENDPOINT")); ep != "" {
		probe.Endpoint = ep
	}
	probe.ResolveAPIKey()
	if probe.APIKey == "" {
		t.Fatalf("no API key for provider %q — set OMLX_API_KEY, SLMCODE_API_KEY, or configure ~/.omlx/settings.json", probe.Provider)
	}
	slmLiveIsolateHome(t)
	apiKey := probe.APIKey

	suite := slmLiveSuiteReport{
		Model:          model,
		Provider:       probe.Provider,
		Endpoint:       probe.Endpoint,
		TaskTimeout:    taskTimeout.String(),
		ScenarioBudget: budget.String(),
		StartedAt:      time.Now().UTC().Format(time.RFC3339),
		Pass:           true,
	}
	suiteStart := time.Now()
	reportPath := strings.TrimSpace(os.Getenv("SLMCODE_E2E_REPORT"))

	// flush rewrites the whole report from what is known so far. It runs after
	// EVERY scenario, not just at the end: this suite can run for hours, and
	// `go test -timeout` expiring kills the process without running deferred
	// functions — an end-only write would throw away four good scenarios
	// because the fifth ran long.
	flush := func() {
		suite.WallSeconds = time.Since(suiteStart).Round(time.Millisecond).Seconds()
		suite.Pass = true
		for _, sc := range suite.Scenarios {
			if !sc.Pass {
				suite.Pass = false
			}
		}
		if reportPath == "" {
			return
		}
		if err := slmLiveWriteReport(reportPath, suite); err != nil {
			t.Errorf("writing %s: %v", reportPath, err)
		}
	}

	defer func() {
		flush()
		t.Log(slmLiveHumanSummary(suite))
		if reportPath != "" {
			t.Logf("machine-readable report: %s", reportPath)
		}
	}()

	for _, sc := range slmLiveScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			// The row is appended from a defer, not from a return value: a
			// t.Fatalf (broken fixture, engine that will not construct) calls
			// runtime.Goexit, and a scenario that vanished from the report is
			// the one failure mode a report must never have.
			rep := &slmLiveScenarioReport{Name: sc.name, Proves: sc.proves, Query: sc.query, Pass: true}
			defer func() {
				rep.Pass = rep.Pass && !t.Failed()
				suite.Scenarios = append(suite.Scenarios, *rep)
				flush()
			}()
			slmLiveRunScenario(t, rep, sc, model, apiKey, taskTimeout, budget)
		})
	}
}

func slmLiveRunScenario(t *testing.T, rep *slmLiveScenarioReport, sc slmLiveScenario, model, apiKey string, taskTimeout, budget time.Duration) {
	t.Helper()

	root := slmLiveWorkspace(t, sc.name)
	rep.Workspace = root
	for _, f := range sc.files {
		slmLiveWrite(t, filepath.Join(root, f.path), f.body)
	}

	// Freeze BEFORE the model sees anything.
	before := map[string]string{}
	for _, rel := range sc.frozen {
		before[rel] = slmLiveSum(t, filepath.Join(root, rel))
	}
	for _, rel := range sc.changed {
		before[rel] = slmLiveSum(t, filepath.Join(root, rel))
	}

	// A fixture that does not start in the state the scenario claims is not
	// testing what it says. This runs before any model call, so it costs
	// nothing and it catches a rotted fixture immediately.
	pre, preOut := slmLiveGoTest(t, root)
	if pre != sc.startsGreen {
		t.Fatalf("fixture precondition broken: `go test ./...` green=%v, want %v\n%s", pre, sc.startsGreen, preOut)
	}

	cfg := slmLiveConfig(t, root, model, apiKey, taskTimeout)
	if err := orchestrator.InitWorkspace(root, cfg); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	h, err := harness.New(root)
	if err != nil {
		t.Fatalf("harness.New: %v", err)
	}
	defer func() { _ = h.Close() }()
	h.Config = cfg
	orch, err := orchestrator.New(cfg)
	if err != nil {
		t.Fatalf("orchestrator.New: %v", err)
	}
	// SetOrchestrator, not a bare assignment: harness.New already built one,
	// and dropping that pointer leaks its MCP subprocesses and evolve store.
	if cerr := h.SetOrchestrator(orch); cerr != nil {
		t.Fatalf("closing the bootstrap orchestrator: %v", cerr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	start := time.Now()
	res, runErr := h.Run(ctx, sc.query)
	wall := time.Since(start)
	rep.WallSeconds = wall.Round(time.Millisecond).Seconds()
	if runErr != nil {
		rep.RunError = runErr.Error()
	}
	if res != nil {
		rep.RunID = res.ID
		rep.EngineSuccess = res.Success
		rep.Tasks = len(res.Board.Tasks)
		rep.FailedTasks = res.FailedTasks
		rep.UnexecutedTasks = res.UnexecutedTasks
		rep.RoleLatencyMs = res.LatencyMs
		if res.Usage != nil {
			rep.TokensIn = res.Usage.PromptTokens
			rep.TokensOut = res.Usage.CompletionTokens
		}
	}
	rep.LLMCalls, rep.ToolCalls, rep.MetricsRow = slmLiveMetrics(root, rep.RunID)
	t.Logf("%s: wall=%s engine_success=%v tasks=%d failed=%d llm_calls=%s tokens=%d/%d",
		sc.name, wall.Round(time.Second), rep.EngineSuccess, rep.Tasks, rep.FailedTasks,
		slmLiveCount(rep.LLMCalls, rep.MetricsRow), rep.TokensIn, rep.TokensOut)

	// ── objective outcomes ──────────────────────────────────────────────────

	// Termination is an assertion, not a formality: `budget` is the ceiling and
	// a run that only stopped because the context expired has not terminated on
	// its own. runErr from a deadline is exactly that case.
	terminated := wall < budget && !slmLiveDeadlineErr(runErr)
	rep.check(t, "terminates-within-budget", terminated,
		"wall=%s budget=%s run_err=%v", wall.Round(time.Second), budget, runErr)

	for _, rel := range sc.frozen {
		after := slmLiveSum(t, filepath.Join(root, rel))
		rep.check(t, "unchanged:"+rel, after == before[rel],
			"sha256 before=%s after=%s", slmLiveShort(before[rel]), slmLiveShort(after))
	}
	for _, rel := range sc.changed {
		after := slmLiveSum(t, filepath.Join(root, rel))
		rep.check(t, "changed:"+rel, after != before[rel],
			"sha256 before=%s after=%s", slmLiveShort(before[rel]), slmLiveShort(after))
	}

	if sc.wantSuccess != nil {
		rep.check(t, "engine-success-is", res != nil && res.Success == *sc.wantSuccess,
			"engine reported success=%v, want %v (failed_tasks=%d)", rep.EngineSuccess, *sc.wantSuccess, rep.FailedTasks)
	}

	if sc.greenAfter {
		if sc.oracle != nil {
			slmLiveWrite(t, filepath.Join(root, sc.oracle.path), sc.oracle.body)
		}
		green, out := slmLiveGoTest(t, root)
		rep.check(t, "go-test-passes", green, "%s", slmLiveClip(out, 4000))
	}
}

// ── plumbing ────────────────────────────────────────────────────────────────

// slmLiveConfig is one place, so every scenario runs the engine the same way:
// real writes, bounded retries, HITL gates answered, and the QA gate pointed at
// the fixture's own test command — which is what a user actually configures.
func slmLiveConfig(t *testing.T, root, model, apiKey string, taskTimeout time.Duration) *config.Config {
	t.Helper()
	cfg := config.Default(root)
	cfg.Model = model
	// The key is injected, not re-resolved: slmLiveIsolateHome has already
	// moved HOME, so ~/.omlx/settings.json is no longer reachable from here.
	cfg.APIKey = apiKey
	if ep := strings.TrimSpace(os.Getenv("SLMCODE_ENDPOINT")); ep != "" {
		cfg.Endpoint = ep
	}
	// NOT testing.Verbose(): the script runs `go test -v` because t.Log output
	// is otherwise swallowed on a passing run, and coupling engine verbosity to
	// that would bury the report under the engine's own trace.
	cfg.Verbose = os.Getenv("SLMCODE_E2E_VERBOSE") == "1"
	// Budget sizing is opt-in (see config.CalibrateBudgets). The suite can turn
	// it on so the two shapes of work — focused and exploratory — are measurable
	// against each other rather than assumed.
	cfg.CalibrateBudgets = os.Getenv("SLMCODE_E2E_CALIBRATE_BUDGETS") == "1"
	cfg.DryRun = false
	cfg.ThinkPasses = 1
	cfg.MaxParallel = 2
	cfg.MaxRetries = 2
	cfg.TaskTimeout = taskTimeout
	cfg.QAGate = true
	cfg.QAGateCommand = "go test ./... -count=1"
	cfg.QAGateMaxRounds = 2
	// No human is attached; a gate that blocks would be scored as a hang.
	cfg.AutoApprove = true
	cfg.EscalateAsk = "auto"
	cfg.ContinueAsk = "auto"
	// Greedy bandit: the model is nondeterministic enough on its own without
	// the self-improvement engine exploring a different arm each run.
	cfg.Deterministic = true
	return cfg
}

// slmLiveIsolateHome points HOME at a throwaway directory for the whole suite.
//
// This is what makes the suite REPEATABLE, and it is not optional. The evolve
// engine's latency memory is user-scoped and cross-project by design
// (pkg/memory/latency.go: "Latencies is user-scoped, cross-project"), living at
// $HOME/.slmcode/memory/latency.json, and the orchestrator derives each role's
// timeout from the p95 in that file. Without isolation:
//
//   - a run INHERITS whatever timings previous runs left behind, so the same
//     scenario gets a different role budget on Tuesday than it did on Monday;
//   - a run POLLUTES the developer's real cross-project memory with fixture
//     timings. A deliberately-truncated debugging run of this very suite wrote
//     six ~4.8s censored explorer samples into the shared store, which halved
//     the measured p95 and starved the next honest run's explorer down to its
//     120s floor — where it promptly timed out and failed the whole scenario.
//
// The suite therefore always starts from the documented COLD START (no
// evidence → every role gets the full task_timeout), which is both
// reproducible and the state a new user is actually in.
//
// GOCACHE/GOMODCACHE/GOPATH are pinned to their real values first: they default
// to paths under $HOME, and every `go test` this suite runs — the fixture
// oracle here, and the harness's own QA gate in a child process — would
// otherwise rebuild the standard library from scratch inside the temp home.
func slmLiveIsolateHome(t *testing.T) {
	t.Helper()
	for _, v := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if os.Getenv(v) != "" {
			continue
		}
		out, err := exec.Command("go", "env", v).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", v, err)
		}
		if val := strings.TrimSpace(string(out)); val != "" {
			t.Setenv(v, val)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Logf("isolated HOME=%s — cross-project latency/evolve memory starts empty every run", home)
}

// slmLiveWorkspace returns the directory a scenario runs in. SLMCODE_E2E_KEEP=1
// opts out of t.TempDir's cleanup so a failure can be opened and read.
func slmLiveWorkspace(t *testing.T, name string) string {
	t.Helper()
	if os.Getenv("SLMCODE_E2E_KEEP") != "1" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("", "slmcode-e2e-slm-"+name+"-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Logf("SLMCODE_E2E_KEEP=1 — workspace retained: %s", dir)
	return dir
}

func slmLiveWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// slmLiveSum is sha256 of a file, or a sentinel for "absent" — deleting a
// frozen file has to read as a change, not as an unreadable file.
func slmLiveSum(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		return "<absent:" + err.Error() + ">"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func slmLiveShort(sum string) string {
	if strings.HasPrefix(sum, "<absent") || len(sum) < 12 {
		return sum
	}
	return sum[:12]
}

// slmLiveGoTest runs the fixture's own suite. This is the oracle: it does not
// care what the model wrote, only whether the code works.
func slmLiveGoTest(t *testing.T, root string) (bool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), slmLiveGoTestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...", "-count=1")
	cmd.Dir = root
	// GOFLAGS from the parent build can carry -mod=vendor and similar, which
	// the fixture module cannot satisfy.
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

// slmLiveMetrics pulls the run's LLM and tool call counts out of the metrics
// row the engine wrote, and reports whether it found one at all.
//
// The row is written by finishEvolveRun at the END of a run, so a run the
// scenario budget cut short has none. That is why `found` exists: a bare 0 in
// the LLM_CALLS column would read as "made no model calls", which is the
// opposite of what a timed-out run did. Missing metrics never fail a scenario —
// they are observability, not the outcome.
func slmLiveMetrics(root, runID string) (llmCalls, toolCalls int, found bool) {
	body, err := os.ReadFile(filepath.Join(root, ".slmcode", "metrics", "runs.jsonl"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			RunID     string `json:"run_id"`
			LLMCalls  int    `json:"llm_calls"`
			ToolCalls int    `json:"tool_calls"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		if runID != "" && row.RunID != runID {
			continue
		}
		llmCalls, toolCalls, found = row.LLMCalls, row.ToolCalls, true
	}
	return llmCalls, toolCalls, found
}

// slmLiveDeadlineErr reports whether err is the scenario budget expiring rather
// than the harness deciding to stop.
func slmLiveDeadlineErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "context canceled")
}

func slmLiveDuration(t *testing.T, env string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q is not a positive Go duration (e.g. 15m): %v", env, raw, err)
	}
	return d
}

// slmLiveCount renders a metrics counter, or "-" when the engine never got far
// enough to write the row. "0" and "unknown" must not print the same.
func slmLiveCount(n int, known bool) string {
	if !known {
		return "-"
	}
	return strconv.Itoa(n)
}

func slmLiveClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}

func slmLiveWriteReport(path string, suite slmLiveSuiteReport) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	body, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func slmLiveHumanSummary(suite slmLiveSuiteReport) string {
	var b strings.Builder
	b.WriteString("\n=== live SLM scenario suite ===\n")
	fmt.Fprintf(&b, "provider=%s model=%s endpoint=%s\n", suite.Provider, suite.Model, suite.Endpoint)
	fmt.Fprintf(&b, "task_timeout=%s scenario_budget=%s total=%.1fs\n\n",
		suite.TaskTimeout, suite.ScenarioBudget, suite.WallSeconds)
	fmt.Fprintf(&b, "%-28s %-6s %9s %6s %6s %9s %9s\n",
		"SCENARIO", "RESULT", "WALL", "TASKS", "CALLS", "TOK_IN", "TOK_OUT")
	for _, sc := range suite.Scenarios {
		verdict := "PASS"
		if !sc.Pass {
			verdict = "FAIL"
		}
		fmt.Fprintf(&b, "%-28s %-6s %8.1fs %6d %6s %9d %9d\n",
			sc.Name, verdict, sc.WallSeconds, sc.Tasks,
			slmLiveCount(sc.LLMCalls, sc.MetricsRow), sc.TokensIn, sc.TokensOut)
	}
	for _, sc := range suite.Scenarios {
		if sc.Pass {
			continue
		}
		fmt.Fprintf(&b, "\n--- %s FAILED (%s) ---\n", sc.Name, sc.Proves)
		for _, c := range sc.Checks {
			if c.Pass {
				continue
			}
			fmt.Fprintf(&b, "  ✖ %s: %s\n", c.Name, slmLiveClip(c.Detail, 1200))
		}
	}
	for _, sc := range suite.Scenarios {
		if len(sc.RoleLatencyMs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\nrole latency — %s (ms)\n", sc.Name)
		keys := make([]string, 0, len(sc.RoleLatencyMs))
		for k := range sc.RoleLatencyMs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-16s %8d\n", k, sc.RoleLatencyMs[k])
		}
	}

	// Marker rows for scripts/e2e-slm.sh. A stable, whitespace-separated line
	// beats teaching a shell script to read JSON without jq, and it keeps the
	// script's table and this one from ever disagreeing.
	b.WriteString("\n")
	passed := 0
	for _, sc := range suite.Scenarios {
		verdict := "FAIL"
		if sc.Pass {
			verdict = "PASS"
			passed++
		}
		fmt.Fprintf(&b, "%s %s %s %.1f %d %s %d %d\n", slmLiveRowMarker, sc.Name, verdict,
			sc.WallSeconds, sc.Tasks, slmLiveCount(sc.LLMCalls, sc.MetricsRow), sc.TokensIn, sc.TokensOut)
	}
	verdict := "FAIL"
	if suite.Pass {
		verdict = "PASS"
	}
	fmt.Fprintf(&b, "%s %s %d %d %.1f\n", slmLiveVerdictMarker, verdict, passed, len(suite.Scenarios), suite.WallSeconds)
	return b.String()
}
