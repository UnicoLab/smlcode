#!/usr/bin/env python3
"""Turn a directory of e2e report JSONs into the tables in docs/slm-learnings.md.

docs/slm-learnings.md is evidence, so it must be reproducible rather than
hand-maintained. Point this at wherever `make e2e-slm` wrote its reports
(SLMCODE_E2E_REPORT) and paste the output into the matching sections.

    python3 scripts/slm-learnings-stats.py ~/slm-reports

Reads every *.json under the directory, keeps the ones carrying the e2e report
shape, and ignores workspace .slmcode state files.
"""
import json
import os
import statistics as st
import sys


def load(root):
    """Every e2e report under root, flattened to one row per scenario run."""
    rows = []
    for dirpath, _dirs, files in os.walk(root):
        if '.slmcode' in dirpath:
            continue
        for fn in files:
            if not fn.endswith('.json'):
                continue
            try:
                with open(os.path.join(dirpath, fn)) as fh:
                    d = json.load(fh)
            except (OSError, ValueError):
                continue
            if not isinstance(d, dict) or 'scenarios' not in d or 'model' not in d:
                continue
            for sc in (d.get('scenarios') or []):
                checks = sc.get('checks') or []
                rows.append(dict(
                    model=d.get('model', '?'),
                    scenario=sc.get('name', '?'),
                    ok=bool(sc.get('pass')),
                    wall=float(sc.get('wall_seconds') or 0),
                    tools=int(sc.get('tool_calls') or 0),
                    llm=int(sc.get('llm_calls') or 0),
                    tin=int(sc.get('tokens_in') or 0),
                    cpass=sum(1 for c in checks if c.get('pass')),
                    ctotal=len(checks),
                    roles=sc.get('role_latency_ms') or {},
                ))
    return rows


def measured(r):
    """A run that emitted a metrics row.

    A run that hit the scenario ceiling first has MISSING call counts, not zero
    ones. Averaging those in understates dispersion badly, so they are counted
    as timeouts and excluded from call-count statistics.
    """
    return r['llm'] > 0 or r['tools'] > 0


def cv(xs):
    """Coefficient of variation — dispersion comparable across models."""
    if len(xs) < 2:
        return float('nan')
    mean = st.mean(xs)
    return st.stdev(xs) / mean if mean else float('nan')


def md_table(headers, aligns, rows):
    out = ['| ' + ' | '.join(headers) + ' |',
           '|' + '|'.join(aligns) + '|']
    out += ['| ' + ' | '.join(str(c) for c in r) + ' |' for r in rows]
    return '\n'.join(out)


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else '.'
    rows = load(root)
    if not rows:
        sys.exit(f"no e2e reports found under {root} — "
                 "run `make e2e-slm` with SLMCODE_E2E_REPORT set")

    models = sorted({r['model'] for r in rows})
    print(f"# {len(rows)} scenario runs across {len(models)} models "
          f"(source: {os.path.abspath(root)})\n")

    # §5 — per-model reliability.
    print("## Per-model\n")
    body = []
    for m in models:
        rs = [r for r in rows if r['model'] == m]
        mr = [r for r in rs if measured(r)]
        tools = [r['tools'] for r in mr] or [0]
        body.append([
            f"`{m}`", len(rs),
            f"**{sum(r['ok'] for r in rs)}/{len(rs)}**",
            f"{sum(r['cpass'] for r in rs)}/{sum(r['ctotal'] for r in rs)}",
            sum(1 for r in rs if not measured(r)),
            f"{st.mean(tools):.0f}",
        ])
    print(md_table(['Model', 'Runs', 'Scenarios passed', 'Checks', 'Timeouts', 'Tools/run'],
                   ['---', '---:', '---:', '---:', '---:', '---:'], body))

    # §4 — dispersion, the finding the harness is designed around.
    print("\n## Dispersion (repeats of one model+scenario)\n")
    pairs = {}
    for r in rows:
        pairs.setdefault((r['model'], r['scenario']), []).append(r)
    body = []
    for (m, s), rs in sorted(pairs.items(), key=lambda kv: -len(kv[1])):
        mr = [r for r in rs if measured(r)]
        if len(mr) < 2:
            continue
        tools = [r['tools'] for r in mr]
        tin = [r['tin'] for r in mr]
        body.append([f"`{m}`", s, len(mr),
                     f"{min(tools)} … {max(tools)}", f"{cv(tools):.2f}",
                     f"{min(tin):,} … {max(tin):,}"])
    print(md_table(['Model', 'Scenario', 'n', 'Tool calls', 'CV', 'Prompt tokens'],
                   ['---', '---', '---:', '---:', '---:', '---'], body))

    # §5 — per-scenario.
    print("\n## Per-scenario\n")
    scen = {}
    for r in rows:
        scen.setdefault(r['scenario'], []).append(r)
    body = []
    for s, rs in sorted(scen.items(), key=lambda kv: -len(kv[1])):
        mr = [r for r in rs if measured(r)] or rs
        body.append([f"`{s}`", len(rs), f"{sum(r['ok'] for r in rs)}",
                     f"{st.median([r['wall'] for r in mr]):.0f} s",
                     f"{st.median([r['tin'] for r in mr]):,.0f}"])
    print(md_table(['Scenario', 'Runs', 'Passed', 'Median wall', 'Median prompt tokens'],
                   ['---', '---:', '---:', '---:', '---:'], body))

    # §7 — where the wall clock goes.
    print("\n## Role latency (median ms)\n")
    roles = {}
    for r in rows:
        for role, ms in (r['roles'] or {}).items():
            roles.setdefault(role, []).append(ms)
    body = [[f"`{role}`", f"{st.median(xs):,.0f} ms", len(xs)]
            for role, xs in sorted(roles.items(), key=lambda kv: -st.median(kv[1]))]
    print(md_table(['Role', 'Median', 'n'], ['---', '---:', '---:'], body))


if __name__ == '__main__':
    main()
