package cli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/UnicoLab/slmcode/pkg/agents"
)

// AgentCmd is a parsed `/agent …` or `/agents` TUI command.
type AgentCmd struct {
	Action string // list | show | new | edit | delete | help
	ID     string
	// Flags from non-interactive / inline form: key=value pairs after the verb.
	Fields map[string]string
}

// ParseAgentCommand parses slash commands for agent CRUD.
//
//	/agents
//	/agent list
//	/agent show <id>
//	/agent new [id=… title=… …]
//	/agent edit <id> [field=value …]
//	/agent delete <id>
func ParseAgentCommand(line string) (AgentCmd, error) {
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return AgentCmd{}, fmt.Errorf("empty command")
	}
	head := strings.ToLower(parts[0])
	switch head {
	case "/agents":
		return AgentCmd{Action: "list"}, nil
	case "/agent":
		// ok
	default:
		return AgentCmd{}, fmt.Errorf("not an agent command")
	}
	if len(parts) == 1 {
		return AgentCmd{Action: "help"}, nil
	}
	action := strings.ToLower(parts[1])
	rest := parts[2:]
	cmd := AgentCmd{Action: action, Fields: map[string]string{}}
	switch action {
	case "list", "ls", "help", "?", "new", "create", "add":
		if action == "ls" {
			cmd.Action = "list"
		}
		if action == "create" || action == "add" {
			cmd.Action = "new"
		}
		if action == "?" {
			cmd.Action = "help"
		}
		cmd.Fields = parseKV(rest)
		if id := cmd.Fields["id"]; id != "" {
			cmd.ID = id
		}
		return cmd, nil
	case "show", "get", "edit", "delete", "rm", "remove":
		if action == "get" {
			cmd.Action = "show"
		}
		if action == "rm" || action == "remove" {
			cmd.Action = "delete"
		}
		if len(rest) == 0 {
			return AgentCmd{}, fmt.Errorf("usage: /agent %s <id>", cmd.Action)
		}
		cmd.ID = strings.ToLower(rest[0])
		cmd.Fields = parseKV(rest[1:])
		return cmd, nil
	default:
		return AgentCmd{}, fmt.Errorf("unknown /agent action %q — try /agent help", action)
	}
}

func parseKV(args []string) map[string]string {
	out := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		// Allow quoted values without a shell: title="Night Auditor"
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

// SpecFromFields builds a CustomSpec from inline key=value fields (+ optional base for edit).
func SpecFromFields(id string, fields map[string]string, base *agents.CustomSpec) agents.CustomSpec {
	var c agents.CustomSpec
	if base != nil {
		c = *base
	}
	if id != "" {
		c.ID = id
	}
	if fields == nil {
		return c
	}
	set := func(key string, dst *string) {
		if v, ok := fields[key]; ok {
			*dst = v
		}
	}
	set("id", &c.ID)
	set("title", &c.Title)
	set("description", &c.Description)
	set("system_prompt", &c.SystemPrompt)
	set("prompt", &c.SystemPrompt)
	set("model", &c.Model)
	set("provider", &c.Provider)
	set("endpoint", &c.Endpoint)
	if v, ok := fields["skills"]; ok {
		c.Skills = splitCSV(v)
	}
	if v, ok := fields["tools"]; ok {
		c.Tools = agents.BoolPtr(parseBool(v, true))
	}
	if v, ok := fields["max_iter"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxIter = n
		}
	}
	if v, ok := fields["max_tokens"]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxTokens = n
		}
	}
	if v, ok := fields["temperature"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Temperature = f
		}
	} else if v, ok := fields["temp"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Temperature = f
		}
	}
	return c
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

// PromptAgentForm walks an interactive wizard (TTY). Blank keeps existing/default.
func PromptAgentForm(in io.Reader, out io.Writer, base agents.CustomSpec, creating bool) (agents.CustomSpec, error) {
	if out == nil {
		out = io.Discard
	}
	sc := bufio.NewScanner(in)
	ask := func(label, cur string) string {
		if cur != "" {
			fmt.Fprintf(out, "  %s [%s]: ", label, cur)
		} else {
			fmt.Fprintf(out, "  %s: ", label)
		}
		if !sc.Scan() {
			return cur
		}
		v := strings.TrimSpace(sc.Text())
		if v == "" {
			return cur
		}
		return v
	}
	c := base
	if creating {
		fmt.Fprintln(out, Bold("New agent")+" — blank = default; builtins can be overridden by id")
		c.ID = ask("id", c.ID)
	} else {
		fmt.Fprintln(out, Bold("Edit agent "+c.ID)+" — blank keeps current")
	}
	c.Title = ask("title", c.Title)
	c.Description = ask("description", c.Description)
	c.Provider = ask("provider", c.Provider)
	c.Model = ask("model", c.Model)
	c.Endpoint = ask("endpoint", c.Endpoint)
	c.SystemPrompt = ask("system_prompt", c.SystemPrompt)
	skills := ask("skills (comma-separated)", strings.Join(c.Skills, ","))
	c.Skills = splitCSV(skills)
	toolsDef := "true"
	if c.Tools != nil && !*c.Tools {
		toolsDef = "false"
	} else if c.Tools == nil && c.Override {
		toolsDef = ""
	}
	toolsIn := ask("tools (true/false)", toolsDef)
	if toolsIn != "" {
		c.Tools = agents.BoolPtr(parseBool(toolsIn, true))
	}
	maxIter := ""
	if c.MaxIter > 0 {
		maxIter = strconv.Itoa(c.MaxIter)
	}
	if v := ask("max_iter", maxIter); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxIter = n
		}
	}
	maxTok := ""
	if c.MaxTokens > 0 {
		maxTok = strconv.Itoa(c.MaxTokens)
	}
	if v := ask("max_tokens", maxTok); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxTokens = n
		}
	}
	temp := ""
	if c.Temperature > 0 {
		temp = strconv.FormatFloat(c.Temperature, 'f', -1, 64)
	}
	if v := ask("temperature", temp); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Temperature = f
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	return c, nil
}

// FormatAgentList renders a compact agent roster for the TUI.
// Pass globalProvider/globalModel so effective LLM (inherit vs pin) is visible.
func FormatAgentList(custom []agents.CustomSpec) string {
	return FormatAgentListWithGlobals(custom, "", "")
}

// FormatAgentListWithGlobals includes effective provider/model after stack inheritance.
func FormatAgentListWithGlobals(custom []agents.CustomSpec, globalProvider, globalModel string) string {
	var b strings.Builder
	for _, a := range agents.PublicSpecsWithCustom(custom) {
		id, _ := a["id"].(string)
		title, _ := a["title"].(string)
		mark := "·"
		if c, _ := a["custom"].(bool); c {
			mark = "★"
		} else if o, _ := a["override"].(bool); o {
			mark = "✎"
		}
		prov, _ := a["provider"].(string)
		model, _ := a["model"].(string)
		effP := strings.TrimSpace(prov)
		if effP == "" {
			effP = globalProvider
		}
		effM := strings.TrimSpace(model)
		if effM == "" {
			effM = globalModel
		}
		extra := ""
		if effP != "" || effM != "" {
			tag := strings.TrimSpace(effP + "/" + effM)
			if strings.TrimSpace(prov) == "" && strings.TrimSpace(model) == "" && (globalProvider != "" || globalModel != "") {
				tag += " (inherit)"
			}
			extra = "  " + Dim(tag)
		}
		b.WriteString(fmt.Sprintf("  %s @%-14s %s%s\n", mark, id, title, extra))
	}
	b.WriteString(Dim("  /agent show|new|edit|delete <id>  ·  inline: /agent new id=foo title=Bar provider=openai endpoint=http://…\n"))
	b.WriteString(Dim("  empty model/provider = inherit active stack / global config\n"))
	return b.String()
}

// FormatAgentShow prints one agent (Studio-parity fields).
func FormatAgentShow(a map[string]interface{}) string {
	if a == nil {
		return "not found\n"
	}
	keys := []string{
		"id", "title", "description", "provider", "model", "endpoint",
		"effective_provider", "effective_model", "inherits_model", "inherits_provider",
		"active_stack", "skills", "tools", "temperature", "max_tokens", "max_iter",
		"system_prompt", "custom", "builtin", "override", "path",
	}
	var b strings.Builder
	for _, k := range keys {
		v, ok := a[k]
		if !ok || v == nil || v == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
	}
	return b.String()
}

// FindPublicAgent returns a public agent map by id.
func FindPublicAgent(custom []agents.CustomSpec, id string) map[string]interface{} {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, a := range agents.PublicSpecsWithCustom(custom) {
		if aid, _ := a["id"].(string); aid == id {
			return a
		}
	}
	return nil
}

// LoadProjectCustoms loads project + global custom agent specs.
func LoadProjectCustoms(agentsDir string) ([]agents.CustomSpec, error) {
	dirs := append([]string{agentsDir}, agents.GlobalAgentRoots()...)
	return agents.LoadCustomSpecs(dirs...)
}
