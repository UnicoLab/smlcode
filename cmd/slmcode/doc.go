// Command slmcode is the SLM-first coding harness CLI.
//
// # Non-interactive contract
//
// Every command is safe to call from a script, a CI job or another agent. The
// rules below are guaranteed:
//
//   - Color. ANSI escapes are emitted only when stdout is a terminal, TERM is
//     not "dumb", and NO_COLOR is unset. `slmcode status | cat` and any redirect
//     to a file are plain text. Override with --color=auto|always|never or
//     FORCE_COLOR=1.
//
//   - JSON. --json is available on status, doctor, readiness, board, version,
//     apply, blocks list, every `config` subcommand, and every `memory`,
//     `evolve` and `metrics` subcommand. It always writes a single JSON
//     document to stdout with color forced off; diagnostics go to stderr.
//
//   - Prompts. Nothing prompts without a TTY. `slmcode apply` refuses
//     interactive review (exit 2) and points at --all/--list/--json; `slmcode`
//     with no workspace refuses to scaffold (exit 3); `slmcode update` needs
//     --yes.
//
//   - HITL gates. With a TTY attached, plan-approve / continue / escalate /
//     clarify gates render inline and block until answered — they never expire
//     into an automatic decision. Without a TTY they resolve immediately using
//     --on-gate-timeout, which defaults to "stop": a plan is never auto-approved
//     in a headless run. Pass --on-gate-timeout=approve to opt into the old
//     permissive behavior, or =reject to fail closed.
//
//   - Rendering verbosity. --log-level=error|warn|info|debug (with -v for info
//     and --vv for debug) decides what the CLI prints. Errors always surface.
//
//   - Errors. A failure is reported exactly once, on stderr, prefixed with "✖".
//
// # Exit codes
//
//	0    success
//	1    generic failure
//	2    usage error / invalid argument / a TTY was required
//	3    workspace not initialized
//	4    provider endpoint unreachable (pre-flight refused to start the run)
//	5    the run completed but tasks failed
//	6    a human-in-the-loop gate could not be answered
//	130  interrupted (SIGINT/SIGTERM); a second interrupt force-quits
//
// # Environment
//
//	SLMCODE_<KEY>
//	    every config key has one, mechanically: SLMCODE_MAX_PARALLEL,
//	    SLMCODE_QA_BOOTSTRAP, SLMCODE_ESCALATE_ASK_TIMEOUT, … Run
//	    `slmcode config schema` for the full list with types and defaults.
//	SLMCODE_PROVIDER, SLMCODE_MODEL, SLMCODE_ENDPOINT, SLMCODE_API_KEY
//	    provider selection; --provider never clobbers an endpoint set by flag,
//	    env, or an explicit non-default config value.
//	SLMCODE_USER_CONFIG, XDG_CONFIG_HOME
//	    location of the user-level config layer (see below).
//	SLMCODE_STUDIO_TOKEN, SLMCODE_STUDIO_NO_AUTH, SLMCODE_STUDIO_DEV_CORS
//	    Studio session-token and CORS overrides (see --no-auth / --dev-cors).
//	SLMCODE_TUI=0, CI=true
//	    force the non-interactive path.
//	SLMCODE_NO_QUIET=1
//	    do not filter dependency stderr during engine construction.
//	SLMCODE_SKIP_UPDATE_CHECK=1
//	    never contact GitHub.
//	NO_COLOR, FORCE_COLOR, TERM
//	    color resolution.
//
// # Configuration layering
//
// Lowest precedence first: built-in defaults → user file → project file
// (.slmcode/config.yaml) → SLMCODE_* environment → command-line flags.
// `slmcode config show --origin` attributes each effective value to
// default | user | project | env SLMCODE_X | flag --x.
//
// The user file is discovered by pkg/config, so the layer applies to Studio,
// the TUI and any embedder as well as the CLI. Candidates, most specific
// first: $SLMCODE_USER_CONFIG, $XDG_CONFIG_HOME/slmcode/config.yaml,
// ~/.slmcode/config.yaml, ~/.config/slmcode/config.yaml. Write to it with
// `slmcode config set --user <key> <value>`.
//
// # Config files record intent
//
// A saved config.yaml holds only the keys that differ from what the project
// would otherwise inherit, plus a `config_version` stamp. Three consequences:
// `config show --origin` can tell a choice from an inherited default, a new
// release's improved default reaches existing projects, and no absolute path
// is embedded in a file that may be committed or copied between machines.
// Older files are migrated forward on load; `slmcode config show` reports when
// that happened.
package main
