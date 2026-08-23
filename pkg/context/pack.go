package contextstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/UnicoLab/slmcode/pkg/context/textutil"
	"github.com/UnicoLab/slmcode/pkg/repomap"
)

// TaskPack is the minimal context handed to one specialist — never the whole repo.
//
// Docs and Files remain maps for JSON/back-compat, but DocOrder and FileOrder
// are the authoritative render order. Ranging a Go map is randomized, so the
// old Render produced a DIFFERENT byte sequence for byte-identical inputs on
// every call — which makes KV-cache prefix reuse impossible. On oMLX/Ollama
// that is the difference between ~0.3s and ~8s time-to-first-token.
type TaskPack struct {
	Query     string            `json:"query"`
	Role      string            `json:"role"`
	TaskID    string            `json:"task_id,omitempty"`
	TaskTitle string            `json:"task_title,omitempty"`
	Docs      map[string]string `json:"docs"`
	Files     map[string]string `json:"files"`
	DocOrder  []string          `json:"doc_order,omitempty"`
	FileOrder []string          `json:"file_order,omitempty"`
	Priority  string            `json:"priority,omitempty"`
	Skills    string            `json:"skills,omitempty"`
	// RepoMap is the ranked symbol index (pkg/repomap) when one is attached.
	RepoMap string `json:"repo_map,omitempty"`
	// Identifiers holds path+signature views for roles packed just-in-time
	// (bodies withheld; the agent pulls them with ws_read on demand).
	Identifiers string `json:"identifiers,omitempty"`

	// BudgetUsed is bytes of content packed (kept for back-compat).
	BudgetUsed int `json:"budget_used"`
	// TokensUsed / BudgetTokens are the real accounting.
	TokensUsed   int  `json:"tokens_used,omitempty"`
	BudgetTokens int  `json:"budget_tokens,omitempty"`
	LeanFiles    bool `json:"-"` // tighter per-file caps for workers
}

const (
	// SafetyMarginPercent is retained for callers that referenced it.
	//
	// Deprecated: the packer now budgets in TOKENS via Budget.Available.
	SafetyMarginPercent = 80

	// MaxLeanPackBytes is retained for callers that referenced it.
	//
	// Deprecated: superseded by RoleBudgetPercent.
	MaxLeanPackBytes = 12 * 1024

	// MinRemainingTokens is the floor below which we stop adding content.
	MinRemainingTokens = 48

	// MinRemainingBytes is the byte-equivalent floor (back-compat).
	MinRemainingBytes = 256

	// MaxSkillFraction is the maximum share of remaining budget given to skills.
	MaxSkillFraction = 15 // percent

	// MaxPriorityTokens caps first-class run handoff/context.
	MaxPriorityTokens = 600

	// MaxPriorityBytes is the byte-equivalent cap (back-compat).
	MaxPriorityBytes = 2400

	// FileFloorPercent is pre-reserved for files before any doc is packed, so
	// one bloated PROJECT.md can never consume the whole budget and leave the
	// specialist with zero code. Applies to every role, not just lean ones.
	FileFloorPercent = 40

	// DocSharePercent caps the share of the budget a SINGLE document may take.
	DocSharePercent = 25

	// FileSharePercent caps the share of the budget a SINGLE file may take.
	FileSharePercent = 45

	// DefaultRepoMapTokens is the repo-map allowance inside a pack.
	DefaultRepoMapTokens = 900

	// MinSkillTokens is the floor below which skills are dropped entirely
	// rather than shipped as a truncated fragment.
	MinSkillTokens = 60

	// MaxCacheEntries bounds the pack cache.
	MaxCacheEntries = 256

	// clipPrecisionBytes bounds the binary search in clipToTokens; tokenizing
	// is not free and byte-exact precision buys nothing.
	clipPrecisionBytes = 64
)

// packerOptions holds everything configurable on a Packer.
type packerOptions struct {
	budget          Budget
	count           TokenCounter
	excerpts        bool
	repo            *repomap.Map
	repoTokens      int
	identifierRoles map[string]bool
	excerptOpts     ExcerptOptions
}

// Option configures a Packer.
type Option func(*packerOptions)

// WithTokenCounter overrides the tokenizer (default: tiktoken via llm.EstimateTokens).
func WithTokenCounter(fn TokenCounter) Option {
	return func(o *packerOptions) {
		if fn != nil {
			o.count = fn
		}
	}
}

// WithBudget replaces the whole reserve model.
func WithBudget(b Budget) Option {
	return func(o *packerOptions) { o.budget = b }
}

// WithReserves overrides the per-request reserves subtracted from the model
// window before the pack gets its share. Pass 0 to keep a default.
func WithReserves(systemTokens, toolTokens, responseTokens int) Option {
	return func(o *packerOptions) {
		if systemTokens > 0 {
			o.budget.ReserveSystemTokens = systemTokens
		}
		if toolTokens > 0 {
			o.budget.ReserveToolTokens = toolTokens
		}
		if responseTokens > 0 {
			o.budget.ReserveResponseTokens = responseTokens
		}
	}
}

// WithRepoMap attaches a repo map. When set, packs carry a ranked symbol index
// (budgeted separately, shrinking as focus files fill the prompt) and
// identifier-only roles get signatures instead of bodies.
func WithRepoMap(m *repomap.Map) Option {
	return func(o *packerOptions) { o.repo = m }
}

// WithRepoMapTokens sets the repo-map allowance (default DefaultRepoMapTokens).
func WithRepoMapTokens(n int) Option {
	return func(o *packerOptions) {
		if n >= 0 {
			o.repoTokens = n
		}
	}
}

// WithExcerpts toggles relevance-windowed file excerpts. Default ON.
func WithExcerpts(on bool) Option {
	return func(o *packerOptions) { o.excerpts = on }
}

// WithExcerptOptions tunes the windowing (±lines, head lines, max windows).
func WithExcerptOptions(eo ExcerptOptions) Option {
	return func(o *packerOptions) { o.excerptOpts = eo }
}

// WithIdentifierOnlyRoles replaces the set of roles that receive path+signature
// identifiers instead of file bodies (just-in-time retrieval discipline).
// Passing no roles disables identifier-only packing entirely.
func WithIdentifierOnlyRoles(roles ...string) Option {
	return func(o *packerOptions) {
		o.identifierRoles = map[string]bool{}
		for _, r := range roles {
			o.identifierRoles[strings.ToLower(strings.TrimSpace(r))] = true
		}
	}
}

// DefaultIdentifierOnlyRoles are the exploratory roles that reason about repo
// SHAPE rather than about specific code. Giving them signatures instead of
// bodies is the single cheapest way to free budget for the roles that edit.
func DefaultIdentifierOnlyRoles() []string {
	return []string{"explorer", "docs", "context", "architect", "coordinator", "memory"}
}

// Packer builds incremental, budgeted context packs from markdown + file excerpts.
type Packer struct {
	Store *Store
	Root  string

	// MaxBytes is the legacy prompt-byte budget.
	//
	// Deprecated: kept so existing callers compile; the packer budgets in
	// tokens. Use NewPackerWithBudget / SetContextLimitTokens.
	MaxBytes int

	opts packerOptions

	cacheMu sync.Mutex
	cache   map[string]*TaskPack // reuse identical packs within a run
	cacheN  []string             // insertion order for bounded eviction
}

func defaultOptions() packerOptions {
	roles := map[string]bool{}
	for _, r := range DefaultIdentifierOnlyRoles() {
		roles[r] = true
	}
	return packerOptions{
		budget:          DefaultBudget(0),
		count:           DefaultTokenCounter,
		excerpts:        true,
		repoTokens:      DefaultRepoMapTokens,
		identifierRoles: roles,
	}
}

// NewPacker keeps the historical constructor working. maxKB is a PROMPT-BYTE
// budget, so it is converted to an approximate token budget (KB*1024/4). Prefer
// NewPackerWithBudget with the model profile's real ContextLimit.
func NewPacker(store *Store, root string, maxKB int) *Packer {
	if maxKB <= 0 {
		maxKB = DefaultMaxContextKB
	}
	p := &Packer{
		Store:    store,
		Root:     root,
		MaxBytes: maxKB * 1024,
		opts:     defaultOptions(),
		cache:    map[string]*TaskPack{},
	}
	// Legacy path: treat the KB figure as the whole window and drop the
	// reserves, otherwise a 16 KB legacy budget would shrink to nothing.
	p.opts.budget = Budget{
		ContextLimitTokens:    TokensFromKB(maxKB),
		ReserveSystemTokens:   1,
		ReserveToolTokens:     1,
		ReserveResponseTokens: 1,
		SlackPercent:          DefaultSlackPercent,
	}
	return p
}

// NewPackerWithBudget is the token-native constructor.
// contextLimitTokens is the model's real window (config.ModelProfile.ContextLimit).
func NewPackerWithBudget(store *Store, root string, contextLimitTokens int, opts ...Option) *Packer {
	p := &Packer{
		Store:    store,
		Root:     root,
		MaxBytes: contextLimitTokens * FallbackCharsPerToken,
		opts:     defaultOptions(),
		cache:    map[string]*TaskPack{},
	}
	p.opts.budget = DefaultBudget(contextLimitTokens)
	for _, o := range opts {
		if o != nil {
			o(&p.opts)
		}
	}
	return p
}

// SetContextLimitTokens updates the model window in place (e.g. after the
// backend reports the resolved model). Clears the cache.
func (p *Packer) SetContextLimitTokens(tokens int) {
	if p == nil || tokens <= 0 {
		return
	}
	p.opts.budget = DefaultBudget(tokens)
	p.ClearCache()
}

// SetRepoMap attaches or replaces the repo map. Clears the cache.
func (p *Packer) SetRepoMap(m *repomap.Map) {
	if p == nil {
		return
	}
	p.opts.repo = m
	p.ClearCache()
}

// BudgetTokensFor reports the token budget a role receives.
func (p *Packer) BudgetTokensFor(role string) int {
	if p == nil {
		return MinPackTokens
	}
	return p.opts.budget.Available(role)
}

// ClearCache drops reused packs (call at the start of each orchestrator Run).
func (p *Packer) ClearCache() {
	if p == nil {
		return
	}
	p.cacheMu.Lock()
	p.cache = map[string]*TaskPack{}
	p.cacheN = nil
	p.cacheMu.Unlock()
}

// BuildRequest is the full input to BuildPack. Query alone is a weak relevance
// signal; TaskTitle/TaskDescription/Acceptance are what the worker was actually
// asked to do and produce far better file excerpts.
type BuildRequest struct {
	Role            string
	Query           string
	TaskID          string
	TaskTitle       string
	TaskDescription string
	Acceptance      string
	Docs            []string
	Files           []string
	SkillsMarkdown  string
	// FocusTerms overrides term extraction when the caller already knows the
	// identifiers in play.
	FocusTerms []string
	// AlreadyInContext lists paths whose bodies the agent already holds; they
	// are excluded from the repo map and shrink its budget.
	AlreadyInContext []string
	// IdentifiersOnly forces just-in-time packing regardless of role.
	IdentifiersOnly bool
	// Bodies forces full-body packing regardless of role.
	Bodies bool
}

// Build creates a role-specific pack (historical signature).
func (p *Packer) Build(role, query string, docNames []string, filePaths []string, skillsMarkdown string) (*TaskPack, error) {
	return p.BuildPack(BuildRequest{
		Role: role, Query: query, Docs: docNames,
		Files: filePaths, SkillsMarkdown: skillsMarkdown,
	})
}

// BuildPack is the full-fidelity entry point.
func (p *Packer) BuildPack(req BuildRequest) (*TaskPack, error) {
	if p == nil {
		return &TaskPack{Docs: map[string]string{}, Files: map[string]string{}}, nil
	}
	priorityMarkdown, skillsMarkdown := splitPriorityMarkdown(req.SkillsMarkdown)
	cacheKey := p.cacheKey(req, priorityMarkdown, skillsMarkdown)
	if cached := p.cacheGet(cacheKey); cached != nil {
		return cached, nil
	}

	pack := &TaskPack{
		Query:     req.Query,
		Role:      req.Role,
		TaskID:    req.TaskID,
		TaskTitle: req.TaskTitle,
		Docs:      map[string]string{},
		Files:     map[string]string{},
	}

	count := p.opts.count
	if count == nil {
		count = DefaultTokenCounter
	}
	budget := p.opts.budget.Available(req.Role)
	pack.BudgetTokens = budget
	used := 0
	lean := isLeanRole(req.Role)
	pack.LeanFiles = lean

	terms := req.FocusTerms
	if len(terms) == 0 {
		terms = ExtractTerms(req.TaskTitle, req.TaskDescription, req.Acceptance, req.Query)
	}

	// Pre-reserve a floor for files so docs can never starve code out.
	fileFloor := budget * FileFloorPercent / 100
	docCap := budget * DocSharePercent / 100
	fileCap := budget * FileSharePercent / 100
	if lean {
		docCap = minInt(docCap, budget/4)
	}
	if docCap < 64 {
		docCap = 64
	}
	if fileCap < 128 {
		fileCap = 128
	}

	remaining := func() int { return budget - used }

	// --- priority (run collaboration contract) ---
	if priorityMarkdown != "" && remaining() > MinRemainingTokens {
		pack.Priority = clipToTokens(priorityMarkdown, minInt(MaxPriorityTokens, remaining()), count)
		used += count(pack.Priority)
	}

	identifiersOnly := req.IdentifiersOnly ||
		(!req.Bodies && p.opts.identifierRoles[strings.ToLower(strings.TrimSpace(req.Role))])

	packFiles := func(reserve int) {
		limit := remaining() - reserve
		if limit <= 0 {
			return
		}
		var ids []string
		for _, rel := range sortedUnique(req.Files) {
			if remaining() <= MinRemainingTokens || limit <= 0 {
				break
			}
			abs := filepath.Join(p.Root, rel)
			data, err := os.ReadFile(abs) //nolint:gosec // paths come from the plan
			if err != nil {
				continue
			}
			if identifiersOnly {
				sig := p.signaturesFor(rel, string(data))
				if strings.TrimSpace(sig) == "" {
					continue
				}
				sig = clipToTokens(sig, minInt(limit, docCap), count)
				ids = append(ids, sig)
				n := count(sig)
				used += n
				limit -= n
				continue
			}
			body := p.excerptFor(string(data), terms, minInt(limit, fileCap), count)
			if strings.TrimSpace(body) == "" {
				continue
			}
			pack.Files[rel] = body
			pack.FileOrder = append(pack.FileOrder, rel)
			n := count(body)
			used += n
			limit -= n
		}
		if len(ids) > 0 {
			pack.Identifiers = strings.Join(ids, "\n")
		}
	}

	packDocs := func() {
		for _, name := range sortedUnique(req.Docs) {
			if remaining() <= MinRemainingTokens {
				break
			}
			body, err := p.Store.Read(name)
			if err != nil {
				continue
			}
			body = strings.TrimSpace(body)
			if body == "" {
				continue
			}
			body = clipToTokens(body, minInt(docCap, remaining()), count)
			if body == "" {
				continue
			}
			pack.Docs[name] = body
			pack.DocOrder = append(pack.DocOrder, name)
			used += count(body)
		}
	}

	// Lean roles: files first (code beats project history). Others: docs first,
	// but always with the file floor reserved.
	if lean {
		packFiles(0)
		packDocs()
	} else {
		docBudget := remaining() - fileFloor
		if docBudget < 0 {
			docBudget = 0
		}
		savedCap := docCap
		docCap = minInt(docCap, docBudget)
		if docCap < 64 {
			docCap = 64
		}
		packDocs()
		docCap = savedCap
		packFiles(0)
	}

	// --- repo map: the shape of everything we did NOT pack ---
	if p.opts.repo != nil && p.opts.repoTokens > 0 && remaining() > MinRemainingTokens {
		inContext := append(append([]string{}, req.AlreadyInContext...), pack.FileOrder...)
		allowance := minInt(p.opts.repoTokens, remaining())
		if rm := p.opts.repo.Render(allowance, inContext); rm != "" {
			pack.RepoMap = rm
			used += count(rm)
		}
	}

	// --- skills: scale cap by remaining budget ---
	if skillsMarkdown != "" && remaining() > MinRemainingTokens {
		skillCap := remaining() * MaxSkillFraction / 100
		if skillCap <= 0 {
			skillCap = remaining()
		}
		skillTokens := count(skillsMarkdown)
		switch {
		case skillTokens <= skillCap:
			// Fits whole — never truncate a short skill pack.
		case skillCap < MinSkillTokens:
			// A 20-token fragment of a behavioral directive is pure noise for
			// a small model: better to ship none than half of one.
			skillCap = 0
		}
		sk := clipToTokens(skillsMarkdown, skillCap, count)
		if strings.TrimSpace(sk) != "" {
			pack.Skills = sk
			used += count(sk)
		}
	}

	pack.TokensUsed = used
	pack.BudgetUsed = packedBytes(pack)
	p.cachePut(cacheKey, pack)
	return copyPack(pack), nil
}

func (p *Packer) signaturesFor(rel, src string) string {
	if p.opts.repo != nil {
		if sig := p.opts.repo.Signatures(rel); strings.TrimSpace(sig) != "" {
			return sig
		}
	}
	return repomap.SignaturesForSource(rel, src)
}

func (p *Packer) excerptFor(content string, terms []string, capTokens int, count TokenCounter) string {
	if capTokens <= 0 {
		return ""
	}
	maxBytes := capTokens * FallbackCharsPerToken
	if !p.opts.excerpts {
		return clipToTokens(content, capTokens, count)
	}
	eo := p.opts.excerptOpts
	eo.MaxBytes = maxBytes
	out := Excerpt(content, terms, eo)
	return clipToTokens(out, capTokens, count)
}

// clipToTokens trims text so count(text) <= capTokens, on a rune boundary.
func clipToTokens(s string, capTokens int, count TokenCounter) string {
	s = strings.TrimSpace(s)
	if s == "" || capTokens <= 0 {
		return ""
	}
	if count == nil {
		count = DefaultTokenCounter
	}
	if count(s) <= capTokens {
		return s
	}
	// Binary search on bytes: token counts are monotonic in prefix length.
	// Seeded from the observed chars-per-token ratio so a big file converges
	// in a handful of tokenizations rather than log2(len).
	lo, hi := 0, len(s)
	if total := count(s); total > 0 {
		guess := capTokens * len(s) / total
		low := guess - guess/4
		high := guess + guess/4 + clipPrecisionBytes
		if low > 0 && low < len(s) && count(textutil.Clip(s, low)) <= capTokens {
			lo = low
		}
		if high < hi && count(textutil.Clip(s, high)) > capTokens {
			hi = high
		}
	}
	for hi-lo > clipPrecisionBytes {
		mid := (lo + hi + 1) / 2
		if count(textutil.Clip(s, mid)) <= capTokens {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	out := textutil.Clip(s, lo)
	if out == "" {
		return ""
	}
	return strings.TrimRight(out, " \t\n") + "\n...[truncated]"
}

func packedBytes(p *TaskPack) int {
	n := len(p.Priority) + len(p.Skills) + len(p.RepoMap) + len(p.Identifiers)
	for _, v := range p.Docs {
		n += len(v)
	}
	for _, v := range p.Files {
		n += len(v)
	}
	return n
}

func (p *Packer) cacheKey(req BuildRequest, priority, skillsMarkdown string) string {
	h := sha256.New()
	// Multi-KB markdown blobs used to be embedded verbatim in the key.
	// h is a hash.Hash: Write never returns an error, so these are safe to ignore.
	_, _ = fmt.Fprintf(h, "role=%s\x00query=%s\x00title=%s\x00desc=%s\x00acc=%s\x00",
		req.Role, req.Query, req.TaskTitle, req.TaskDescription, req.Acceptance)
	_, _ = fmt.Fprintf(h, "docs=%s\x00files=%s\x00ctx=%s\x00terms=%s\x00",
		strings.Join(sortedUnique(req.Docs), ","),
		strings.Join(sortedUnique(req.Files), ","),
		strings.Join(sortedUnique(req.AlreadyInContext), ","),
		strings.Join(req.FocusTerms, ","))
	_, _ = fmt.Fprintf(h, "ids=%v\x00bodies=%v\x00budget=%d\x00",
		req.IdentifiersOnly, req.Bodies, p.opts.budget.Available(req.Role))
	sum := sha256.Sum256([]byte(priority))
	h.Write(sum[:])
	sum = sha256.Sum256([]byte(skillsMarkdown))
	h.Write(sum[:])
	h.Write([]byte(p.freshnessKey(req.Docs, req.Files)))
	return hex.EncodeToString(h.Sum(nil))
}

func (p *Packer) cacheGet(key string) *TaskPack {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if cached, ok := p.cache[key]; ok && cached != nil {
		return copyPack(cached)
	}
	return nil
}

func (p *Packer) cachePut(key string, pack *TaskPack) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.cache == nil {
		p.cache = map[string]*TaskPack{}
	}
	if _, exists := p.cache[key]; !exists {
		p.cacheN = append(p.cacheN, key)
		for len(p.cacheN) > MaxCacheEntries {
			oldest := p.cacheN[0]
			p.cacheN = p.cacheN[1:]
			delete(p.cache, oldest)
		}
	}
	p.cache[key] = copyPack(pack)
}

func (p *Packer) freshnessKey(docNames []string, filePaths []string) string {
	if p == nil {
		return ""
	}
	// h is a hash.Hash: Write/Fprintf never return an error, so these are safe to ignore.
	h := sha256.New()
	for _, name := range sortedUnique(docNames) {
		_, _ = fmt.Fprintf(h, "doc:%s:", name)
		if p.Store == nil {
			h.Write([]byte("no-store;"))
			continue
		}
		data, err := os.ReadFile(p.Store.Path(name))
		if err != nil {
			_, _ = fmt.Fprintf(h, "err:%v;", err)
			continue
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
	}
	for _, rel := range sortedUnique(filePaths) {
		_, _ = fmt.Fprintf(h, "file:%s:", filepath.ToSlash(rel))
		data, err := os.ReadFile(filepath.Join(p.Root, rel)) //nolint:gosec // plan-supplied
		if err != nil {
			_, _ = fmt.Fprintf(h, "err:%v;", err)
			continue
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func copyPack(in *TaskPack) *TaskPack {
	if in == nil {
		return nil
	}
	out := *in
	out.Docs = copyStringMap(in.Docs)
	out.Files = copyStringMap(in.Files)
	out.DocOrder = append([]string(nil), in.DocOrder...)
	out.FileOrder = append([]string(nil), in.FileOrder...)
	return &out
}

func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// sortedUnique returns a deterministic, de-duplicated view of in.
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// splitPriorityMarkdown separates the run collaboration contract from ordinary
// skills markdown. The marker no longer has to sit at byte 0 — a single
// leading heading or newline used to silently disable priority protection.
func splitPriorityMarkdown(markdown string) (priority, rest string) {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return "", ""
	}
	const marker = "## Run collaboration contract"
	idx := strings.Index(markdown, marker)
	if idx < 0 {
		return "", markdown
	}
	prefix := strings.TrimSpace(markdown[:idx])
	tail := markdown[idx:]
	next := strings.Index(tail[len(marker):], "\n## ")
	if next < 0 {
		return strings.TrimSpace(tail), prefix
	}
	cut := len(marker) + next + 1
	priority = strings.TrimSpace(tail[:cut])
	rest = strings.TrimSpace(tail[cut:])
	if prefix != "" {
		rest = strings.TrimSpace(prefix + "\n\n" + rest)
	}
	return priority, rest
}

// TakePriority clips the run collaboration contract to a byte budget. Exported
// for callers that build a contract block outside the packer.
func TakePriority(markdown string, remaining int) string {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" || remaining <= 0 {
		return ""
	}
	limit := remaining
	if limit > MaxPriorityBytes {
		limit = MaxPriorityBytes
	}
	return textutil.TruncateDefault(markdown, limit)
}

func isLeanRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "worker", "corrector", "deep", "reviewer", "tester",
		"planner", "splitter", "coordinator", "architect", "context", "memory":
		return true
	default:
		return false
	}
}

// Render turns a pack into a prompt section for a specialist.
//
// Order is MOST-STABLE-FIRST so an SLM server can reuse its KV cache across
// calls within a run: role header → skills → project docs → repo map →
// identifiers → files (sorted) → run contract → task → user query. The
// volatile query used to sit at the FRONT, which invalidated the entire prefix
// on every single call. The per-call "(context budget used: N bytes)" footer is
// gone for the same reason — it sat between the context and the request and
// changed every time.
func (p *TaskPack) Render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Scoped context for role=%s\n\n", p.Role))

	if p.Skills != "" {
		b.WriteString(p.Skills)
		b.WriteString("\n\n")
	}
	for _, name := range p.renderDocOrder() {
		body := p.Docs[name]
		if strings.TrimSpace(body) == "" {
			continue
		}
		fmt.Fprintf(&b, "## Doc: %s\n\n%s\n\n", name, body)
	}
	if p.RepoMap != "" {
		b.WriteString(p.RepoMap)
		b.WriteString("\n\n")
	}
	if p.Identifiers != "" {
		b.WriteString("## File identifiers (bodies withheld — use ws_read to open one)\n\n```\n")
		b.WriteString(p.Identifiers)
		b.WriteString("\n```\n\n")
	}
	for _, name := range p.renderFileOrder() {
		body := p.Files[name]
		if strings.TrimSpace(body) == "" {
			continue
		}
		fmt.Fprintf(&b, "## File: %s\n\n```\n%s\n```\n\n", name, body)
	}
	if p.Priority != "" {
		b.WriteString(p.Priority)
		b.WriteString("\n\n")
	}
	if p.TaskID != "" {
		fmt.Fprintf(&b, "Task: %s — %s\n\n", p.TaskID, p.TaskTitle)
	}
	if p.Query != "" {
		b.WriteString("## User query\n\n")
		b.WriteString(p.Query)
		b.WriteString("\n\n")
	}
	return b.String()
}

// renderDocOrder returns the deterministic doc order: PROJECT.md first (it is
// the most stable document in the workspace and therefore the best prefix),
// then the recorded order, then any map-only leftovers sorted by name.
func (p *TaskPack) renderDocOrder() []string {
	return orderedKeys(p.DocOrder, p.Docs, DocProject)
}

func (p *TaskPack) renderFileOrder() []string {
	// Files always render sorted by path: stable across calls and across
	// however the planner happened to order them.
	return sortedUnique(orderedKeys(p.FileOrder, p.Files))
}

func orderedKeys(order []string, m map[string]string, first ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(m))
	for _, f := range first {
		if _, ok := m[f]; ok && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, k := range order {
		if _, ok := m[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// DefaultDocsForRole picks which markdown docs a specialist typically needs.
func DefaultDocsForRole(role string) []string {
	switch role {
	case "context":
		return []string{DocProject, DocContext, DocQuery}
	case "explorer":
		return []string{DocProject, DocQuery, DocContext}
	case "docs":
		return []string{DocProject, DocQuery, DocContext}
	case "architect", "coordinator":
		return []string{DocQuery, DocContext, DocPlan}
	case "planner":
		return []string{DocQuery, DocContext, DocProject}
	case "splitter":
		return []string{DocPlan, DocQuery}
	case "worker", "corrector", "deep", "placeholder":
		return []string{DocQuery, DocContext}
	case "reviewer":
		return []string{DocQuery}
	case "tester":
		return []string{DocProject, DocTasks}
	case "memory":
		return []string{DocMemory, DocPlan}
	default:
		return []string{DocQuery, DocContext}
	}
}

// LeanDocsForRole returns a minimal doc set for execute-time / multi-turn packs.
func LeanDocsForRole(role string) []string {
	switch role {
	case "worker", "corrector", "deep", "placeholder":
		return []string{DocQuery, DocContext}
	case "planner", "splitter", "architect":
		return []string{DocQuery, DocContext}
	case "reviewer", "coordinator":
		return []string{DocQuery}
	case "tester":
		return []string{DocProject}
	default:
		return DefaultDocsForRole(role)
	}
}
