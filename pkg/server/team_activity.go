package server

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/UnicoLab/slmcode/pkg/orchestrator"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/session"
	"github.com/UnicoLab/slmcode/pkg/squads"
	"github.com/UnicoLab/slmcode/pkg/stream"
)

// ── How the teams worked together ────────────────────────────────────────
//
// Everything a project manager decided, every handoff between teams and every
// gate a half passed or failed is already in the event stream — interleaved
// with a thousand tool calls and token deltas. Nobody reads it there. The
// question the Teams page has to answer after a run is "what did the managers
// DO, and how did the teams collaborate", and the answer is this derived
// timeline: the team-relevant events only, each classified (a triage verdict,
// a reassignment, a contract clause, a stall, a gate) and stamped with the team
// it concerns, so it can be filtered by team and read top to bottom.
//
// Derived, not stored. The events are the record; a second store would be a
// second thing to keep in sync with them.

// Activity kinds, in the vocabulary the page groups by.
const (
	ActivitySelection   = "selection"   // which teams, and why
	ActivityContract    = "contract"    // a clause frozen, or the contract attached
	ActivityRouting     = "routing"     // a task given to a team or a specialist
	ActivityTriage      = "triage"      // a manager's verdict on a rejected delivery
	ActivityReassign    = "reassign"    // a task moved to another agent
	ActivityStall       = "stall"       // a team waiting on another's interface
	ActivityWave        = "wave"        // who is working, and for which team
	ActivityGate        = "gate"        // a team's acceptance result
	ActivityIntegration = "integration" // joining the halves
	ActivityProgress    = "progress"    // per-team progress between waves
	ActivityEdit        = "edit"        // a human changed the org chart
)

// ActivityEntry is one line of the timeline.
type ActivityEntry struct {
	Time    string `json:"time"`
	Kind    string `json:"kind"`
	Phase   string `json:"phase,omitempty"`
	Level   string `json:"level,omitempty"`
	Team    string `json:"team,omitempty"`
	Agent   string `json:"agent,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Message string `json:"message"`
}

// activitySource is the subset of an event the classifier reads, so the live
// ring buffer (stream.Event) and a past run's JSONL (session.EventRecord) go
// through the same code.
type activitySource struct {
	Time    string
	Phase   string
	Kind    string
	Level   string
	Agent   string
	TaskID  string
	Message string
}

var (
	reTeamWord    = regexp.MustCompile(`\b(?:team|squad) ([a-z0-9][a-z0-9-]*)`)
	reWaitingOn   = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*) is waiting on`)
	reProposes    = regexp.MustCompile(`^(\S+) proposes (\S+)`)
	reReassigned  = regexp.MustCompile(`^(\S+) reassigned from (\S+) to (\S+)`)
	reFrozen      = regexp.MustCompile(`^frozen: `)
	reWave        = regexp.MustCompile(`^wave \d+: `)
	reAssigned    = regexp.MustCompile(`^assigned `)
	reHandAssign  = regexp.MustCompile(`^(\S+) assigned to team ([a-z0-9][a-z0-9-]*)`)
	reTeamPrefix  = regexp.MustCompile(`^(?:team|squad) ([a-z0-9][a-z0-9-]*)`)
	reTeamLibrary = regexp.MustCompile(`^team library:`)
	reProvidedBy  = regexp.MustCompile(`provided by ([a-z0-9][a-z0-9-]*)`)
)

// classifyActivity decides whether one event belongs on the timeline and, if
// so, what it is. Returns ok=false for everything that is not about teams.
//
// The patterns are the exact shapes the orchestrator and loop emit (see
// pkg/orchestrator/squads.go, teams.go, teamgate.go and pkg/loop/squads.go).
// A message that changes there and not here falls off the timeline rather
// than being misfiled — the tests pin the ones that matter.
func classifyActivity(ev activitySource) (string, bool) {
	msg := strings.TrimSpace(ev.Message)
	if msg == "" || ev.Kind == stream.KindToken || ev.Kind == stream.KindTool {
		return "", false
	}
	lower := strings.ToLower(msg)
	switch ev.Phase {
	case "charter":
		switch {
		case reFrozen.MatchString(lower), strings.HasPrefix(lower, "freezing the interface"),
			strings.HasPrefix(lower, "contract attached"):
			return ActivityContract, true
		case strings.HasPrefix(lower, "squad assignment:"), strings.Contains(lower, "spans both squads"),
			strings.Contains(lower, "no squad owns"), strings.Contains(lower, "cut along"),
			strings.Contains(lower, "dropped the dependency"), strings.Contains(lower, "no longer waits"):
			return ActivityRouting, true
		case strings.HasPrefix(lower, "squad plan edited"), strings.HasPrefix(lower, "teams activated"),
			reHandAssign.MatchString(lower):
			return ActivityEdit, true
		default:
			return ActivitySelection, true
		}
	case "coord":
		if reProposes.MatchString(msg) {
			return ActivityTriage, true
		}
		if strings.Contains(lower, "triage") || strings.Contains(lower, "project manager") {
			return ActivityTriage, true
		}
	case "plan":
		switch {
		case reReassigned.MatchString(msg):
			return ActivityReassign, true
		case strings.Contains(lower, "triage ignored"):
			return ActivityTriage, true
		case strings.HasPrefix(lower, "squad edit"), strings.HasPrefix(lower, "squad plan edited"):
			return ActivityEdit, true
		}
	case "split":
		if reAssigned.MatchString(lower) && ev.TaskID != "" {
			return ActivityRouting, true
		}
		if strings.HasPrefix(lower, "routed ") {
			return ActivityRouting, true
		}
	case "execute":
		switch {
		case strings.HasPrefix(lower, "squads: "):
			return ActivityProgress, true
		case strings.Contains(lower, " is waiting on "):
			return ActivityStall, true
		case reWave.MatchString(lower) && strings.Contains(lower, "team"):
			return ActivityWave, true
		case reReassigned.MatchString(msg):
			return ActivityReassign, true
		case strings.HasPrefix(lower, "re-assigned "), strings.HasPrefix(lower, "re-staffed wave"):
			return ActivityReassign, true
		}
	case "verify":
		if strings.HasPrefix(lower, "team ") || strings.Contains(lower, "for team ") ||
			strings.Contains(lower, "team stamp") {
			return ActivityGate, true
		}
	case "integrate":
		return ActivityIntegration, true
	}
	// Loop-level events carry no team phase; the wave announcement is the one
	// that names teams.
	if ev.Kind == stream.KindCoord && reWave.MatchString(lower) && strings.Contains(lower, "team") {
		return ActivityWave, true
	}
	if reReassigned.MatchString(msg) {
		return ActivityReassign, true
	}
	return "", false
}

// teamOf names the team an entry concerns, from the strongest signal down: a
// task the board has stamped, a "team X" / "squad X" mention, a stall's
// subject, then any plan team id mentioned as a word.
func teamOf(ev activitySource, kind string, taskTeams map[string]string, teamIDs []string) string {
	if ev.TaskID != "" {
		if t := taskTeams[ev.TaskID]; t != "" {
			return t
		}
	}
	lower := strings.ToLower(ev.Message)
	// A captured word is a team only if the plan says so — "team library",
	// "squad assignment" and "squad plan" are all "team <word>" to a regex.
	// With no plan to check against, the words those messages use are
	// excluded by name.
	known := func(id string) bool {
		if len(teamIDs) > 0 {
			for _, t := range teamIDs {
				if t == id {
					return true
				}
			}
			return false
		}
		return !notATeamWord[id]
	}
	if m := reHandAssign.FindStringSubmatch(lower); m != nil && known(m[2]) {
		return m[2]
	}
	if m := reTeamPrefix.FindStringSubmatch(lower); m != nil && known(m[1]) {
		return m[1]
	}
	if kind == ActivityStall {
		if m := reWaitingOn.FindStringSubmatch(lower); m != nil && known(m[1]) {
			return m[1]
		}
	}
	// A contract clause belongs to the team that owes it.
	if kind == ActivityContract {
		if m := reProvidedBy.FindStringSubmatch(lower); m != nil && known(m[1]) {
			return m[1]
		}
	}
	if m := reTeamWord.FindStringSubmatch(lower); m != nil && !reTeamLibrary.MatchString(lower) && known(m[1]) {
		return m[1]
	}
	// A line naming exactly one team is about that team; a line naming two is
	// about the run and gets no stamp, or "2 teams selected: a, b" would be
	// filed under a.
	var mentioned []string
	for _, id := range teamIDs {
		if containsWordASCII(lower, id) {
			mentioned = append(mentioned, id)
		}
	}
	if len(mentioned) == 1 {
		return mentioned[0]
	}
	return ""
}

// notATeamWord is the vocabulary that follows "team"/"squad" in the messages
// the orchestrator emits about ALL teams at once.
var notATeamWord = map[string]bool{
	"library": true, "assignment": true, "plan": true, "plans": true, "is": true, "are": true,
	"gates": true, "gate": true, "stamp": true, "stamps": true, "edit": true, "edits": true,
	"was": true, "has": true, "of": true, "and": true, "the": true, "structure": true,
}

func containsWordASCII(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !isWordByte(haystack[i-1])
		after := i+len(needle) >= len(haystack) || !isWordByte(haystack[i+len(needle)])
		if before && after {
			return true
		}
		from = i + 1
		if from >= len(haystack) {
			return false
		}
	}
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
}

// deriveTeamActivity turns a run's events into the team timeline.
func deriveTeamActivity(events []activitySource, p *squads.Plan, taskTeams map[string]string) []ActivityEntry {
	var ids []string
	if p != nil {
		for _, s := range p.Squads {
			ids = append(ids, s.ID)
		}
	}
	out := make([]ActivityEntry, 0, 32)
	for _, ev := range events {
		kind, ok := classifyActivity(ev)
		if !ok {
			continue
		}
		e := ActivityEntry{
			Time:    ev.Time,
			Kind:    kind,
			Phase:   ev.Phase,
			Level:   ev.Level,
			Agent:   ev.Agent,
			TaskID:  ev.TaskID,
			Message: strings.TrimSpace(ev.Message),
			Team:    teamOf(ev, kind, taskTeams, ids),
		}
		// A triage verdict is announced by the manager that gave it, and a
		// reassignment by the agent that received it; the agent field on the
		// event already says which. Nothing to rewrite — but the "manager"
		// specialist that assembles teams is the run's charter voice, not a
		// team's manager, and is dropped so the filter "decisions by X" is not
		// polluted with selection lines.
		if kind == ActivitySelection && e.Agent == "manager" {
			e.Agent = ""
		}
		out = append(out, e)
	}
	return out
}

// ManagerSummary is one manager's record on this run.
type ManagerSummary struct {
	Team      string `json:"team"`
	Manager   string `json:"manager"`
	Default   bool   `json:"default,omitempty"`
	Decisions int    `json:"decisions"`
	Moved     int    `json:"moved"`
	Stalls    int    `json:"stalls"`
	Gate      string `json:"gate,omitempty"` // green | red | unverified | ""
}

// summarizeManagers folds the timeline into one row per team.
func summarizeManagers(p *squads.Plan, entries []ActivityEntry, gates []orchestrator.TeamGate, defaultManager string) []ManagerSummary {
	if p == nil {
		return nil
	}
	rows := make([]ManagerSummary, 0, len(p.Squads))
	index := map[string]int{}
	for _, s := range p.Squads {
		row := ManagerSummary{Team: s.ID, Manager: strings.TrimSpace(s.Manager)}
		if row.Manager == "" {
			row.Manager, row.Default = defaultManager, true
		}
		index[s.ID] = len(rows)
		rows = append(rows, row)
	}
	for _, e := range entries {
		i, ok := index[e.Team]
		if !ok {
			continue
		}
		switch e.Kind {
		case ActivityTriage:
			rows[i].Decisions++
		case ActivityReassign:
			rows[i].Moved++
		case ActivityStall:
			rows[i].Stalls++
		}
	}
	for _, g := range gates {
		i, ok := index[g.Team]
		if !ok {
			continue
		}
		switch {
		case !g.Ran:
			rows[i].Gate = "unverified"
		case g.OK:
			rows[i].Gate = "green"
		default:
			rows[i].Gate = "red"
		}
	}
	return rows
}

// handleTeamActivity serves the timeline for the current run, or for a past
// one when ?query=<id> names it.
func (s *Server) handleTeamActivity(w http.ResponseWriter, r *http.Request) {
	limit := 400
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var sources []activitySource
	queryID := strings.TrimSpace(r.URL.Query().Get("query"))
	var cached *activityCacheEntry
	if queryID != "" {
		// A past run's log is immutable once the run ended, and Studio
		// re-requests this view on every Teams-page visit. The derived
		// timeline is cached per (query, size, mtime) of the log so the
		// second visit is a map lookup, not a re-parse of a 250k-line file.
		if entry, ok := s.cachedActivity(queryID); ok {
			cached = entry
		} else {
			// ReadEvents keeps the FIRST n records; a run that streamed tokens
			// has tens of thousands, and the gates and late triage that matter
			// most sit at the end. Read the whole log.
			recs, err := session.ReadEvents(s.slmDir(), queryID, pastRunEventCap)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, rec := range recs {
				sources = append(sources, activitySource{
					Time: rec.Time, Phase: rec.Phase, Kind: rec.Kind, Agent: rec.Agent,
					TaskID: rec.TaskID, Message: rec.Message,
				})
			}
		}
	} else {
		s.mu.Lock()
		for _, se := range s.events {
			if se.Seq < s.runStartSeq {
				continue // previous run; the ring is no longer cleared per run
			}
			sources = append(sources, activitySource{
				Time: se.Event.Time.Format(time.RFC3339Nano), Phase: se.Event.Phase, Kind: se.Event.Kind,
				Level: se.Event.Level, Agent: se.Event.Agent, TaskID: se.Event.TaskID, Message: se.Event.Message,
			})
		}
		s.mu.Unlock()
	}

	tasks := s.boardTasks()
	if queryID != "" {
		if turn, err := session.LoadTurn(s.slmDir(), queryID); err == nil && turn != nil {
			tasks = turn.Board.Tasks
		}
	}
	var planPtr *squads.Plan
	if p, ok, err := squads.Load(s.slmDir()); err == nil && ok && len(p.Squads) > 0 && planBelongsToBoard(&p, tasks) {
		planPtr = &p
	}
	taskTeams := map[string]string{}
	for _, t := range tasks {
		if t.Squad != "" {
			taskTeams[t.ID] = t.Squad
		}
	}

	var entries []ActivityEntry
	if cached != nil {
		entries = cached.entries
	} else {
		entries = deriveTeamActivity(sources, planPtr, taskTeams)
		if queryID != "" {
			s.storeActivity(queryID, entries)
		}
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	counts := map[string]int{}
	for _, e := range entries {
		counts[e.Kind]++
	}
	var gates []orchestrator.TeamGate
	if o := s.orch(); o != nil && queryID == "" {
		gates = o.TeamGates()
	}
	teamIDs := []string{}
	if planPtr != nil {
		teamIDs = planPtr.IDs()
	}
	sort.Strings(teamIDs)
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"query":    queryID,
		"teams":    teamIDs,
		"entries":  entries,
		"counts":   counts,
		"managers": summarizeManagers(planPtr, entries, gates, runDefaultManager),
		"tasks":    teamTaskRows(tasks),
	})
}

// pastRunEventCap bounds a past run's event log read for the timeline.
const pastRunEventCap = 250000

// activityCacheEntry is one derived timeline keyed by the event log's identity.
type activityCacheEntry struct {
	size    int64
	modTime time.Time
	entries []ActivityEntry
}

// maxActivityCache bounds the per-query cache; past this the oldest entry by
// insertion is dropped (a re-derive, exactly today's cost).
const maxActivityCache = 32

// cachedActivity returns the derived timeline for queryID when the event log
// still has the size and mtime it was derived from.
func (s *Server) cachedActivity(queryID string) (*activityCacheEntry, bool) {
	info, err := os.Stat(session.EventsPath(s.slmDir(), queryID)) //nolint:gosec // session.TurnDir sanitizes the id; the path cannot leave the queries dir
	if err != nil {
		return nil, false
	}
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	e, ok := s.activityCache[queryID]
	if !ok || e.size != info.Size() || !e.modTime.Equal(info.ModTime()) {
		return nil, false
	}
	return e, true
}

func (s *Server) storeActivity(queryID string, entries []ActivityEntry) {
	info, err := os.Stat(session.EventsPath(s.slmDir(), queryID)) //nolint:gosec // session.TurnDir sanitizes the id; the path cannot leave the queries dir
	if err != nil {
		return
	}
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	if s.activityCache == nil {
		s.activityCache = map[string]*activityCacheEntry{}
		s.activityOrder = nil
	}
	if _, exists := s.activityCache[queryID]; !exists {
		s.activityOrder = append(s.activityOrder, queryID)
		for len(s.activityOrder) > maxActivityCache {
			delete(s.activityCache, s.activityOrder[0])
			s.activityOrder = s.activityOrder[1:]
		}
	}
	s.activityCache[queryID] = &activityCacheEntry{size: info.Size(), modTime: info.ModTime(), entries: entries}
}

// planBelongsToBoard says whether the saved org chart is the one this board
// was built under.
//
// The plan on disk is a PROJECT fact — written by the last run that had
// teams, or by Activate on the Teams page — and the run whose events are
// being read may have had none. The board is the arbiter, the same way
// Resume decides (orchestrator.restoreSquadPlan): a board with tasks and no
// team stamp from this plan ran without it, and showing its managers would
// report two managers for a run that had none. An EMPTY board is the
// pre-run state after Activate, where the chart is exactly what to show.
func planBelongsToBoard(p *squads.Plan, tasks []plan.Task) bool {
	if p == nil {
		return false
	}
	if len(tasks) == 0 {
		return true
	}
	for _, t := range tasks {
		if _, on := p.Squad(t.Squad); t.Squad != "" && on {
			return true
		}
	}
	return false
}

// teamTaskRows is the board seen by team: which tasks each team holds, who is
// on them and where they stand. The board page has the cards; this is the
// roll-up the Teams page needs to say "backend has four, two blocked".
func teamTaskRows(tasks []plan.Task) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tasks))
	// Unassigned tasks are listed too: the seam is real work, and a roll-up
	// that hid it would under-count the run.
	for _, t := range tasks {
		out = append(out, map[string]interface{}{
			"id":     t.ID,
			"title":  t.Title,
			"team":   t.Squad,
			"role":   t.Role,
			"column": t.Column,
			"status": t.Status,
		})
	}
	return out
}
