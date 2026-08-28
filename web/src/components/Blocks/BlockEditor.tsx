// ── BlockEditor — kind-aware modal for creating/editing blocks ──
// Supports pack, pipeline, agent, and quality blocks. Meta fields are shared;
// the `spec` section is rendered per kind. Validation happens client-side for
// the common rules; backend errors (400/409) are surfaced inline.
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { createBlock, getBlock, getBlocks, getAgents, updateBlock } from '@/api/client';
import type {
  AgentBlockSpec,
  BlockCatalogEntry,
  AgentSpec,
  BlockPayload,
  ExecuteLoop,
  GroupMeta,
  PackBlockSpec,
  PhaseSpec,
  PipelineConfig,
  QualityBlockSpec,
  QualityCheckCmd,
  Slot,
} from '@/types';
import {
  AlertCircle,
  Archive,
  Bot,
  ChevronDown,
  ChevronUp,
  Code2,
  Loader2,
  Package,
  PenLine,
  Plus,
  RotateCcw,
  Save,
  ShieldCheck,
  Trash2,
  Workflow,
  X,
} from 'lucide-react';
import clsx from 'clsx';
import {
  CheckboxField,
  CmdListEditor,
  CommaField,
  NumberField,
  Section,
  SelectField,
  SuggestInput,
  TextArea,
  TextField,
  splitComma,
} from './fields';

export const BLOCK_ID_RE = /^[a-z][a-z0-9_-]{1,63}$/;

const KIND_TITLES: Record<string, string> = { pack: 'Pack', pipeline: 'Pipeline', agent: 'Agent', quality: 'Quality' };

const KIND_ICONS: Record<string, ReactNode> = {
  pack: <Package size={18} />,
  pipeline: <Workflow size={18} />,
  agent: <Bot size={18} />,
  quality: <ShieldCheck size={18} />,
};

const KIND_ICON_COLORS: Record<string, string> = {
  pack: 'text-amber-500',
  pipeline: 'text-sky-500',
  agent: 'text-violet-500',
  quality: 'text-emerald-500',
};

// ── Minimal YAML serialize/parse (no dependencies) ──────────────────────────
// The backend accepts JSON bodies only, so YAML mode round-trips the draft
// through a tiny YAML subset: nested maps, plain/quoted scalars, `- item`
// lists, flow lists, and `|`/`|-` block scalars. That covers every shape the
// block editors produce. Parsing is tolerant — on error we surface an inline
// message and block saving.

const YAML_QUOTE_RE = /[:#[\]{}&*!|>'"%@`,\n\r]/;
const YAML_NUMERIC_RE = /^[-+]?(\d+\.?\d*|\.\d+)/;
const YAML_BOOLISH_RE = /^(true|false|null|~|yes|no|on|off|y|n)$/i;
const YAML_CHOMP_RE = /^(\||\|-|\|\+|>|>-|>\+)$/;

/** Quote a scalar when plain YAML would change its type or break parsing. */
function yamlQuote(s: string): string {
  if (s === '') return '""';
  if (/^\s|\s$/.test(s)) return JSON.stringify(s);
  if (YAML_QUOTE_RE.test(s)) return JSON.stringify(s);
  if (YAML_NUMERIC_RE.test(s)) return JSON.stringify(s);
  if (YAML_BOOLISH_RE.test(s)) return JSON.stringify(s);
  if (/^[-?](\s|$)/.test(s)) return JSON.stringify(s);
  return s;
}

function isEmittable(v: unknown): boolean {
  if (v === undefined || v === null) return false;
  if (typeof v === 'string') return v !== '';
  if (Array.isArray(v)) return true; // keep empty arrays (round-trips to `[]`, not undefined)
  if (typeof v === 'object') return Object.keys(v).length > 0;
  if (typeof v === 'number') return Number.isFinite(v);
  return true;
}

function emitBlockBody(s: string, indent: number, lines: string[]): void {
  const normalized = s.replace(/\r\n/g, '\n');
  const pad = ' '.repeat(indent);
  const parts = normalized.split('\n');
  if (normalized.endsWith('\n')) parts.pop();
  for (const part of parts) lines.push(part === '' ? pad : `${pad}${part}`);
  if (normalized.endsWith('\n')) lines.push(pad); // keep exactly one trailing newline
}

function emitArray(arr: unknown[], itemIndent: number, lines: string[]): void {
  const pad = ' '.repeat(itemIndent);
  for (const item of arr) {
    if (item === undefined || item === null) continue;
    if (typeof item === 'object') {
      emitObject(item as Record<string, unknown>, itemIndent, true, lines);
    } else if (typeof item === 'string' && item.includes('\n')) {
      lines.push(`${pad}- ${item.endsWith('\n') ? '|' : '|-'}`);
      emitBlockBody(item, itemIndent + 2, lines);
    } else if (typeof item === 'string') {
      lines.push(`${pad}- ${yamlQuote(item)}`);
    } else {
      lines.push(`${pad}- ${String(item)}`);
    }
  }
}

function emitObject(obj: Record<string, unknown>, indent: number, dash: boolean, lines: string[]): void {
  const entries = Object.entries(obj).filter(([, v]) => isEmittable(v));
  if (entries.length === 0) {
    lines.push(`${' '.repeat(indent)}${dash ? '- {}' : '{}'}`);
    return;
  }
  const keyIndent = dash ? indent + 2 : indent;
  const pad = ' '.repeat(keyIndent);
  entries.forEach(([key, val], i) => {
    const lead = dash && i === 0 ? ' '.repeat(indent) + '- ' : pad;
    emitEntry(key, val, keyIndent, lead, lines);
  });
}

function emitEntry(key: string, val: unknown, keyIndent: number, lead: string, lines: string[]): void {
  const k = yamlQuote(key);
  if (typeof val === 'string' && val.includes('\n')) {
    lines.push(`${lead}${k}: ${val.endsWith('\n') ? '|' : '|-'}`);
    emitBlockBody(val, keyIndent + 2, lines);
  } else if (Array.isArray(val)) {
    lines.push(`${lead}${k}:${val.length === 0 ? ' []' : ''}`);
    if (val.length > 0) emitArray(val, keyIndent + 2, lines);
  } else if (typeof val === 'object') {
    lines.push(`${lead}${k}:`);
    emitObject(val as Record<string, unknown>, keyIndent + 2, false, lines);
  } else if (typeof val === 'string') {
    lines.push(`${lead}${k}: ${yamlQuote(val)}`);
  } else {
    lines.push(`${lead}${k}: ${String(val)}`);
  }
}

/** Serialize a plain object to 2-space indented YAML. */
function objectToYaml(obj: Record<string, unknown>): string {
  const lines: string[] = [];
  emitObject(obj, 0, false, lines);
  return lines.join('\n');
}

/** Parse the YAML subset back into a plain object. Throws on malformed input. */
function yamlToObject(text: string): Record<string, unknown> {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  let i = 0;

  const nextContentLine = (): { indent: number; trimmed: string } | null => {
    for (let j = i; j < lines.length; j++) {
      const t = lines[j].trim();
      if (t === '' || t.startsWith('#')) continue;
      return { indent: lines[j].length - lines[j].trimStart().length, trimmed: t };
    }
    return null;
  };

  const yamlScalarValue = (s: string): unknown => {
    const raw = s.trim();
    if (raw.startsWith('"') && raw.endsWith('"')) {
      try {
        return JSON.parse(raw);
      } catch {
        /* malformed quote — fall through to plain */
      }
    } else if (raw.startsWith("'") && raw.endsWith("'")) {
      return raw.slice(1, -1).replace(/''/g, "'");
    }
    const t = raw.replace(/\s+#.*$/, '').trim();
    if (t === '') return '';
    if (t === 'true') return true;
    if (t === 'false') return false;
    if (t === 'null' || t === '~') return null;
    if (/^[-+]?\d+$/.test(t)) return parseInt(t, 10);
    if (/^[-+]?\d*\.\d+$/.test(t)) return parseFloat(t);
    return t;
  };

  const parseBlockScalar = (keyIndent: number, chomp: string): string => {
    const content: string[] = [];
    let base = -1;
    while (i < lines.length) {
      const raw = lines[i];
      const trimmed = raw.trim();
      if (trimmed === '') {
        if (base >= 0) content.push('');
        i++;
        continue;
      }
      const ind = raw.length - raw.trimStart().length;
      if (base < 0) {
        if (ind <= keyIndent) break; // no content
        base = ind;
        content.push(raw.slice(ind));
        i++;
        continue;
      }
      if (ind < base) break;
      content.push(raw.slice(base));
      i++;
    }
    if (content.length === 0) return '';
    while (content.length > 0 && content[content.length - 1] === '') content.pop();
    const joined = content.join('\n');
    return chomp === '|' || chomp === '>' ? joined + '\n' : joined;
  };

  const parseFlowList = (s: string): unknown[] => {
    const end = s.indexOf(']');
    const inner = (end === -1 ? s.slice(1) : s.slice(1, end)).trim();
    if (inner === '') return [];
    const items: string[] = [];
    let cur = '';
    let q: string | null = null;
    for (const ch of inner) {
      if (q) {
        cur += ch;
        if (ch === q) q = null;
      } else if (ch === '"' || ch === "'") {
        q = ch;
        cur += ch;
      } else if (ch === ',') {
        items.push(cur);
        cur = '';
      } else {
        cur += ch;
      }
    }
    if (cur.trim() !== '' || items.length === 0) items.push(cur);
    return items.map((x) => yamlScalarValue(x.trim())).filter((x) => x !== null);
  };

  const parseInlineValue = (v: string, keyIndent: number): unknown => {
    const t = v.trim();
    if (t === '') {
      const next = nextContentLine();
      if (next && next.indent > keyIndent) {
        return next.trimmed.startsWith('- ') ? parseList(next.indent) : parseNode(next.indent);
      }
      return '';
    }
    if (YAML_CHOMP_RE.test(t)) return parseBlockScalar(keyIndent, t);
    if (t.startsWith('[')) return parseFlowList(t);
    if (t === '{}') return {};
    return yamlScalarValue(t);
  };

  const parseNode = (indent: number): Record<string, unknown> => {
    const obj: Record<string, unknown> = {};
    while (i < lines.length) {
      const raw = lines[i];
      const trimmed = raw.trim();
      if (trimmed === '' || trimmed.startsWith('#')) {
        i++;
        continue;
      }
      const curIndent = raw.length - raw.trimStart().length;
      if (curIndent < indent) break;
      if (curIndent > indent) throw new Error(`unexpected indentation at line ${i + 1}`);
      const m = trimmed.match(/^([^:]+):(?:\s*)(.*)$/);
      if (!m) throw new Error(`expected "key: value" at line ${i + 1}`);
      const key = yamlScalarValue(m[1].trim());
      if (typeof key !== 'string' || key === '') throw new Error(`invalid key at line ${i + 1}`);
      i++;
      obj[key] = parseInlineValue(m[2], indent);
    }
    return obj;
  };

  const parseList = (indent: number): unknown[] => {
    const items: unknown[] = [];
    while (i < lines.length) {
      const raw = lines[i];
      const trimmed = raw.trim();
      if (trimmed === '' || trimmed.startsWith('#')) {
        i++;
        continue;
      }
      const curIndent = raw.length - raw.trimStart().length;
      if (curIndent < indent) break;
      if (curIndent > indent) throw new Error(`unexpected indentation at line ${i + 1}`);
      if (!trimmed.startsWith('- ')) break;
      const rest = trimmed.slice(2);
      i++;
      if (rest === '') {
        const next = nextContentLine();
        items.push(
          next && next.indent > indent ? (next.trimmed.startsWith('- ') ? parseList(next.indent) : parseNode(next.indent)) : '',
        );
      } else if (rest.startsWith('"') || rest.startsWith("'")) {
        items.push(yamlScalarValue(rest));
      } else if (YAML_CHOMP_RE.test(rest)) {
        items.push(parseBlockScalar(indent, rest));
      } else if (rest.startsWith('[')) {
        items.push(parseFlowList(rest));
      } else {
        const m = rest.match(/^([^:]+):(?:\s*)(.*)$/);
        if (m) {
          const key = yamlScalarValue(m[1].trim());
          if (typeof key !== 'string' || key === '') throw new Error(`invalid key at line ${i}`);
          const item: Record<string, unknown> = { [key]: parseInlineValue(m[2], indent + 2) };
          const next = nextContentLine();
          if (next && next.indent > indent && !next.trimmed.startsWith('- ')) {
            Object.assign(item, parseNode(next.indent));
          }
          items.push(item);
        } else {
          items.push(yamlScalarValue(rest));
        }
      }
    }
    return items;
  };

  const first = nextContentLine();
  if (!first) return {};
  if (first.trimmed.startsWith('- ')) throw new Error('top level must be a mapping, not a list');
  return parseNode(first.indent);
}

interface MetaState {
  id: string;
  name: string;
  description: string;
  version: string;
  author: string;
  license: string;
  language: string;
  icon: string;
  tags: string[];
  shareable: boolean;
}

function defaultMeta(): MetaState {
  return {
    id: '',
    name: '',
    description: '',
    version: '1.0.0',
    author: 'UnicoLab',
    license: 'MIT',
    language: '',
    icon: '',
    tags: [],
    shareable: true,
  };
}

// ── Kind-specific default specs (create mode) ──
function defaultAgentSpec(): AgentBlockSpec {
  return {
    id: '',
    title: '',
    description: '',
    system_prompt: '',
    skills: [],
    model: '',
    provider: '',
    endpoint: '',
    tools: true,
    max_iter: 10,
    temperature: 0.2,
    max_tokens: 2048,
  };
}

function defaultQualitySpec(): QualityBlockSpec {
  return {
    detect: { files: [], extensions: [], priority: 20 },
    format: [],
    lint: [],
    typecheck: [],
    test: [],
    build: [],
    smoke: '',
    qa_gate: '',
    safe_prefixes: [],
    tester_hints: '',
  };
}

function defaultPackSpec(): PackBlockSpec {
  return {
    pipeline: '',
    quality: '',
    agents: [],
    skills: [],
    pin_skills: false,
    override_tester: '',
    override_worker: '',
    defer_plan_approve: false,
    defer_clarify: false,
  };
}

// Mirrors the engine's default 16-phase graph so a new pipeline is a working
// starting point (the backend merges defaults for missing phases anyway).
function defaultPipelineSpec(): PipelineConfig {
  return {
    version: 1,
    order: [
      'init', 'skills', 'context', 'explore', 'docs', 'architect',
      'clarify', 'plan', 'split', 'coord', 'execute', 'learn',
      'polish', 'test', 'memory', 'done',
    ],
    groups: [
      { id: 'prepare', label: 'Prepare', steps: ['init', 'skills', 'context', 'explore', 'docs'] },
      { id: 'design', label: 'Design', steps: ['architect', 'clarify', 'plan', 'split'] },
      { id: 'build', label: 'Build', steps: ['coord', 'execute', 'learn'] },
      { id: 'verify', label: 'Verify', steps: ['polish', 'test'] },
      { id: 'finish', label: 'Finish', steps: ['memory', 'done'] },
    ],
    phases: {
      init: { agent: '', when: 'always', label: 'Init', tip: 'Boot workspace + session', group: 'prepare', enabled: true },
      skills: { agent: '', when: 'always', label: 'Skills', tip: 'Load skills & knowledge packs', group: 'prepare' },
      context: { agent: 'context', when: 'always', label: 'Context', tip: 'Refresh CONTEXT / project memory', group: 'prepare' },
      explore: { agent: 'explorer', when: 'auto', label: 'Explore', tip: 'Discover relevant files', group: 'prepare' },
      docs: { agent: 'docs', when: 'auto', label: 'Docs', tip: 'Read docs & conventions', group: 'prepare' },
      architect: { agent: 'architect', when: 'auto', label: 'Architect', tip: 'Shape approach & components', group: 'design' },
      clarify: { agent: '', when: 'auto', label: 'Clarify', tip: 'Lock PRD / ask decisions', group: 'design' },
      plan: { agent: 'planner', when: 'always', label: 'Plan', tip: 'Write the execution plan', group: 'design' },
      split: { agent: 'splitter', when: 'always', label: 'Split', tip: 'Break into atomic tasks', group: 'design' },
      coord: { agent: 'coordinator', when: 'always', label: 'Coord', tip: 'Coordinate board & focus', group: 'build' },
      execute: { agent: 'worker', when: 'always', label: 'Execute', tip: 'Workers implement + review', group: 'build' },
      learn: { agent: 'memory', when: 'auto', label: 'Learn', tip: 'Capture lessons mid-run', group: 'build' },
      polish: { agent: 'placeholder', when: 'always', label: 'Polish', tip: 'Fill placeholders / flag gaps', group: 'verify' },
      test: { agent: 'tester', when: 'always', label: 'Test', tip: 'Tester + QA gate verification', group: 'verify' },
      memory: { agent: 'memory', when: 'always', label: 'Memory', tip: 'Distill long-term memory', group: 'finish' },
      done: { agent: '', when: 'always', label: 'Done', tip: 'Run complete', group: 'finish' },
    },
    execute: { default_role: 'worker', reviewer: 'reviewer', corrector: 'corrector', max_waves: 2 },
    slots: [],
  };
}

function defaultSpecFor(kind: string): unknown {
  switch (kind) {
    case 'agent':
      return defaultAgentSpec();
    case 'quality':
      return defaultQualitySpec();
    case 'pack':
      return defaultPackSpec();
    case 'pipeline':
      return defaultPipelineSpec();
    default:
      return {};
  }
}

// Converts either a full block (with spec) or a catalog entry into form state.
function blockToState(b: unknown): { meta: MetaState; spec: unknown } {
  const obj = (b ?? {}) as Record<string, unknown>;
  return {
    meta: {
      id: (obj.id as string) ?? '',
      name: (obj.name as string) ?? '',
      description: (obj.description as string) ?? '',
      version: (obj.version as string) ?? '1.0.0',
      author: (obj.author as string) ?? 'UnicoLab',
      license: (obj.license as string) ?? 'MIT',
      language: (obj.language as string) ?? '',
      icon: (obj.icon as string) ?? '',
      tags: Array.isArray(obj.tags) ? (obj.tags as string[]) : [],
      shareable: obj.shareable !== false,
    },
    spec: obj.spec,
  };
}

function cleanError(e: unknown): string {
  const msg = e instanceof Error ? e.message : 'Save failed';
  return msg.replace(/^Error:\s*/, '').replace(/^\d{3}:\s*/, '');
}

interface Refs {
  agents: AgentSpec[];
  pipelines: BlockCatalogEntry[];
  quality: BlockCatalogEntry[];
  agentBlocks: BlockCatalogEntry[];
}

interface BlockEditorProps {
  open: boolean;
  kind: string;
  mode: 'create' | 'edit';
  /** Catalog entry used for edit mode (fallback + detail fetch key). */
  block?: BlockCatalogEntry | null;
  onClose: () => void;
  onSaved: (kind: string, id: string, created: boolean) => void;
}

export default function BlockEditor({ open, kind, mode, block, onClose, onSaved }: BlockEditorProps) {
  const [meta, setMeta] = useState<MetaState>(defaultMeta);
  const [spec, setSpec] = useState<unknown>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [refs, setRefs] = useState<Refs>({ agents: [], pipelines: [], quality: [], agentBlocks: [] });
  const [view, setView] = useState<'form' | 'yaml'>('form');
  const [yamlDraft, setYamlDraft] = useState<string | null>(null);
  const [yamlError, setYamlError] = useState<string | null>(null);

  // Reset form when the editor opens; for edit mode load the full block detail.
  useEffect(() => {
    if (!open) return;
    setError(null);
    setSaving(false);
    setView('form');
    setYamlDraft(null);
    setYamlError(null);
    setMeta(defaultMeta());
    if (mode === 'create') {
      setSpec(defaultSpecFor(kind));
      setDetailLoading(false);
      return;
    }
    setSpec(null);
    setDetailLoading(true);
    (async () => {
      try {
        const full = await getBlock(kind, block?.id ?? '');
        const st = blockToState(full);
        setMeta(st.meta);
        setSpec(st.spec ?? defaultSpecFor(kind));
      } catch (e) {
        console.error('Failed to load block detail, falling back to catalog entry:', e);
        const st = blockToState(block ?? null);
        setMeta(st.meta);
        setSpec(st.spec ?? defaultSpecFor(kind));
      } finally {
        setDetailLoading(false);
      }
    })();
  }, [open, kind, mode, block]);

  // Load reference data for kind-specific selects (agents for pipeline,
  // other blocks for pack) lazily when the editor opens.
  useEffect(() => {
    if (!open || (kind !== 'pipeline' && kind !== 'pack')) return;
    let cancelled = false;
    (async () => {
      try {
        if (kind === 'pipeline') {
          const [agents, agentBlocks] = await Promise.all([getAgents(), getBlocks('agent')]);
          if (!cancelled) setRefs((r) => ({ ...r, agents: agents || [], agentBlocks: agentBlocks?.blocks || [] }));
        } else {
          const [pipelines, quality, agentBlocks] = await Promise.all([
            getBlocks('pipeline'),
            getBlocks('quality'),
            getBlocks('agent'),
          ]);
          if (!cancelled) {
            setRefs((r) => ({
              ...r,
              pipelines: pipelines?.blocks || [],
              quality: quality?.blocks || [],
              agentBlocks: agentBlocks?.blocks || [],
            }));
          }
        }
      } catch (e) {
        console.error('Failed to load block references:', e);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, kind]);

  // Close on Escape.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const agentOptions = useMemo(() => {
    const set = new Set<string>();
    refs.agents.forEach((a) => a.id && set.add(a.id));
    refs.agentBlocks.forEach((b) => set.add(b.id));
    return [...set].sort();
  }, [refs]);

  const setMetaField = <K extends keyof MetaState>(key: K, value: MetaState[K]) =>
    setMeta((m) => ({ ...m, [key]: value }));

  // ── Raw YAML mode ──
  const buildYamlDraft = (): string => {
    const payload: Record<string, unknown> = {
      api_version: 'blocks/v1',
      kind,
      ...(meta.id.trim() ? { id: meta.id.trim() } : {}),
      ...(meta.name.trim() ? { name: meta.name.trim() } : {}),
      ...(meta.description.trim() ? { description: meta.description.trim() } : {}),
      ...(meta.version.trim() ? { version: meta.version.trim() } : {}),
      ...(meta.author.trim() ? { author: meta.author.trim() } : {}),
      ...(meta.license.trim() ? { license: meta.license.trim() } : {}),
      ...(meta.language.trim() ? { language: meta.language.trim() } : {}),
      ...(meta.icon.trim() ? { icon: meta.icon.trim() } : {}),
      ...(meta.tags.length > 0 ? { tags: meta.tags } : {}),
      shareable: meta.shareable,
      spec: spec ?? {},
    };
    return objectToYaml(payload);
  };

  const switchToYaml = () => {
    if (view === 'yaml') return;
    setYamlDraft(buildYamlDraft());
    setYamlError(null);
    setView('yaml');
  };

  const switchToForm = () => {
    if (view !== 'yaml') return;
    if (yamlDraft !== null) {
      try {
        const parsed = yamlToObject(yamlDraft);
        const st = blockToState(parsed);
        setMeta(st.meta);
        if (st.spec !== undefined && st.spec !== null) setSpec(st.spec);
        setYamlError(null);
      } catch (e) {
        setYamlError(`Can't parse YAML: ${e instanceof Error ? e.message : 'unknown error'}`);
        return; // stay in YAML mode until the draft parses
      }
    }
    setView('form');
  };

  // ── Payload building + validation ──
  const buildPipelinePayload = (): PipelineConfig | null => {
    const p = spec as PipelineConfig;
    const phases: Record<string, PhaseSpec> = {};
    for (const [pid, ps] of Object.entries(p.phases ?? {})) {
      phases[pid] = {
        agent: (ps.agent ?? '').trim(),
        when: ps.when || 'always',
        label: (ps.label ?? '').trim() || pid,
        tip: (ps.tip ?? '').trim(),
        group: (ps.group ?? '').trim(),
        ...(ps.enabled !== undefined ? { enabled: ps.enabled } : {}),
      };
    }
    const groups: GroupMeta[] = [];
    const seenGroup = new Set<string>();
    for (const g of p.groups ?? []) {
      const gid = g.id.trim();
      if (!gid) {
        setError('Every group needs an id');
        return null;
      }
      if (seenGroup.has(gid)) {
        setError(`Duplicate group id "${gid}"`);
        return null;
      }
      seenGroup.add(gid);
      const steps = [...new Set((g.steps ?? []).map((s) => s.trim().toLowerCase()).filter(Boolean))];
      for (const s of steps) {
        if (!phases[s]) {
          phases[s] = { agent: '', when: 'always', label: s, tip: '', group: gid, enabled: true };
        }
        phases[s] = { ...phases[s], group: gid };
      }
      groups.push({ id: gid, label: (g.label ?? '').trim() || gid, steps });
    }
    const grouped = new Set(groups.flatMap((g) => g.steps));
    const orphans = Object.keys(phases).filter((pid) => !grouped.has(pid));
    const order = [...groups.flatMap((g) => g.steps), ...orphans];
    if (order.length === 0) {
      setError('Pipeline needs at least one phase');
      return null;
    }
    for (const pid of order) {
      if (!BLOCK_ID_RE.test(pid)) {
        setError(`Invalid phase id "${pid}" (use lowercase letters, digits, _ or -)`);
        return null;
      }
    }
    const slots = (p.slots ?? []).map((s) => ({
      id: (s.id ?? '').trim().toLowerCase(),
      title: (s.title ?? '').trim(),
      agent: (s.agent ?? '').trim().toLowerCase(),
      before: (s.before ?? '').trim().toLowerCase(),
      after: (s.after ?? '').trim().toLowerCase(),
      replace: (s.replace ?? '').trim().toLowerCase(),
      when: s.when || 'always',
      input: s.input ?? '',
      persist_to: s.persist_to || 'scratch',
      fail_mode: s.fail_mode || 'continue',
      ...(s.enabled !== undefined ? { enabled: s.enabled } : {}),
    }));
    const seenSlot = new Set<string>();
    for (const s of slots) {
      if (!BLOCK_ID_RE.test(s.id)) {
        setError(`Slot "${s.id || '(empty)'}": invalid id (use lowercase letters, digits, _ or -)`);
        return null;
      }
      if (seenSlot.has(s.id)) {
        setError(`Duplicate slot id "${s.id}"`);
        return null;
      }
      seenSlot.add(s.id);
      if (!s.agent) {
        setError(`Slot "${s.id}": agent is required`);
        return null;
      }
      if (!s.before && !s.after && !s.replace) {
        setError(`Slot "${s.id}": set before, after, or replace`);
        return null;
      }
      for (const anchor of [s.before, s.after, s.replace]) {
        if (anchor && !order.includes(anchor)) {
          setError(`Slot "${s.id}": unknown phase anchor "${anchor}"`);
          return null;
        }
      }
    }
    return {
      version: p.version || 1,
      phases,
      order,
      groups,
      execute: {
        default_role: (p.execute?.default_role ?? '').trim(),
        reviewer: (p.execute?.reviewer ?? '').trim(),
        corrector: (p.execute?.corrector ?? '').trim(),
        ...(p.execute?.max_waves ? { max_waves: p.execute.max_waves } : {}),
      },
      slots,
    };
  };

  const buildPayload = (): BlockPayload | null => {
    const id = meta.id.trim().toLowerCase();
    if (!BLOCK_ID_RE.test(id)) {
      setError('Invalid id: use lowercase letters, digits, _ or - (2–64 chars)');
      return null;
    }
    if (!meta.name.trim()) {
      setError('Name is required');
      return null;
    }
    const base: BlockPayload = {
      api_version: 'blocks/v1',
      kind,
      id,
      name: meta.name.trim(),
      description: meta.description.trim() || undefined,
      version: meta.version.trim() || undefined,
      author: meta.author.trim() || undefined,
      license: meta.license.trim() || undefined,
      language: meta.language.trim() || undefined,
      icon: meta.icon.trim() || undefined,
      tags: meta.tags,
      shareable: meta.shareable,
    };

    switch (kind) {
      case 'pipeline': {
        const built = buildPipelinePayload();
        if (!built) return null;
        return { ...base, spec: built };
      }
      case 'agent': {
        const a = (spec ?? {}) as AgentBlockSpec;
        return {
          ...base,
          spec: {
            ...a,
            id,
            title: (a.title ?? '').trim() || meta.name.trim(),
            description: (a.description ?? '').trim(),
            system_prompt: a.system_prompt ?? '',
            skills: a.skills ?? [],
            max_iter: a.max_iter ?? 10,
            temperature: a.temperature ?? 0.2,
            max_tokens: a.max_tokens ?? 2048,
            model: (a.model ?? '').trim() || undefined,
            provider: (a.provider ?? '').trim() || undefined,
            endpoint: (a.endpoint ?? '').trim() || undefined,
          },
        };
      }
      case 'quality': {
        const q = (spec ?? {}) as QualityBlockSpec;
        const cmds = (rows?: QualityCheckCmd[]) =>
          (rows ?? [])
            .filter((r) => r.cmd.trim() !== '')
            .map((r) => ({
              cmd: r.cmd.trim(),
              ...(r.label?.trim() ? { label: r.label.trim() } : {}),
              ...(r.optional ? { optional: true } : {}),
            }));
        const hasGate = q.qa_gate?.trim() || q.smoke?.trim() || (q.test ?? []).some((r) => r.cmd.trim());
        if (!hasGate) {
          setError('Quality block needs at least a qa_gate, smoke, or test command');
          return null;
        }
        return {
          ...base,
          spec: {
            detect: {
              files: q.detect?.files ?? [],
              extensions: q.detect?.extensions ?? [],
              priority: q.detect?.priority || 20,
            },
            format: cmds(q.format),
            lint: cmds(q.lint),
            typecheck: cmds(q.typecheck),
            test: cmds(q.test),
            build: cmds(q.build),
            smoke: q.smoke?.trim() || undefined,
            qa_gate: q.qa_gate?.trim() || undefined,
            safe_prefixes: q.safe_prefixes ?? [],
            tester_hints: q.tester_hints?.trim() || undefined,
          },
        };
      }
      case 'pack': {
        const pk = (spec ?? {}) as PackBlockSpec;
        if (!pk.pipeline?.trim() && !pk.quality?.trim() && (pk.agents ?? []).length === 0) {
          setError('Pack must reference at least one pipeline, quality, or agent');
          return null;
        }
        return {
          ...base,
          spec: {
            pipeline: pk.pipeline?.trim() || undefined,
            quality: pk.quality?.trim() || undefined,
            agents: pk.agents ?? [],
            skills: pk.skills ?? [],
            pin_skills: !!pk.pin_skills,
            override_tester: pk.override_tester?.trim() || undefined,
            override_worker: pk.override_worker?.trim() || undefined,
            defer_plan_approve: !!pk.defer_plan_approve,
            defer_clarify: !!pk.defer_clarify,
          },
        };
      }
      default:
        setError(`Unsupported block kind "${kind}"`);
        return null;
    }
  };

  const handleSave = async () => {
    setError(null);
    let payload: BlockPayload | null = null;
    if (view === 'yaml') {
      if (!yamlDraft || yamlDraft.trim() === '') {
        setError('YAML is empty — nothing to save');
        return;
      }
      try {
        const parsed = yamlToObject(yamlDraft);
        const id = String(parsed.id ?? '').trim().toLowerCase();
        if (!BLOCK_ID_RE.test(id)) {
          setError('Invalid id: use lowercase letters, digits, _ or - (2–64 chars)');
          return;
        }
        if (!String(parsed.name ?? '').trim()) {
          setError('Name is required');
          return;
        }
        payload = {
          ...parsed,
          api_version: 'blocks/v1',
          kind,
          id,
          spec: parsed.spec ?? spec ?? defaultSpecFor(kind),
        } as unknown as BlockPayload;
      } catch (e) {
        setError(`Invalid YAML: ${e instanceof Error ? e.message : 'could not parse'}`);
        return;
      }
    } else {
      payload = buildPayload();
      if (!payload) return;
    }
    setSaving(true);
    try {
      if (mode === 'create') {
        await createBlock(kind, payload);
      } else {
        await updateBlock(kind, payload.id, payload);
      }
      onSaved(kind, payload.id, mode === 'create');
    } catch (e) {
      console.error('Block save failed:', e);
      setError(cleanError(e));
      setSaving(false);
    }
  };

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 backdrop-blur-sm sm:p-6"
      role="button"
      tabIndex={0}
      aria-label="Close dialog"
      onMouseDown={onClose}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClose();
        }
      }}
    >
      <div
        className={clsx(
          'card my-4 flex max-h-[88vh] w-full flex-col overflow-hidden',
          kind === 'pipeline' ? 'max-w-4xl' : 'max-w-2xl',
        )}
        role="button"
        tabIndex={0}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between gap-3 border-b border-gray-200 px-6 py-4 dark:border-gray-800">
          <h2 className="flex items-center gap-2 font-bold text-gray-900 dark:text-white">
            <span className={KIND_ICON_COLORS[kind] || 'text-gray-400'}>{KIND_ICONS[kind]}</span>
            {mode === 'create' ? `New ${KIND_TITLES[kind] || kind}` : `Edit ${KIND_TITLES[kind] || kind}`}
            {mode === 'edit' && (
              <span className="badge-neutral font-mono text-[10px]">{meta.id || block?.id || ''}</span>
            )}
          </h2>
          <div className="flex shrink-0 items-center gap-3">
            <div className="flex items-center rounded-lg border border-gray-200 bg-gray-50 p-0.5 dark:border-gray-800 dark:bg-gray-900">
              <button
                onClick={switchToForm}
                disabled={detailLoading}
                title="Structured form"
                className={clsx(
                  'flex items-center gap-1 rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                  view === 'form'
                    ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-white'
                    : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300',
                  detailLoading && 'cursor-not-allowed opacity-50',
                )}
              >
                <PenLine size={13} />
                Form
              </button>
              <button
                onClick={switchToYaml}
                disabled={detailLoading}
                title="Raw YAML"
                className={clsx(
                  'flex items-center gap-1 rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                  view === 'yaml'
                    ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-white'
                    : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300',
                  detailLoading && 'cursor-not-allowed opacity-50',
                )}
              >
                <Code2 size={13} />
                YAML
              </button>
            </div>
            <button onClick={onClose} className="btn-ghost rounded-lg p-1.5" title="Close">
              <X size={16} />
            </button>
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 space-y-4 overflow-y-auto px-6 py-4">
          {error && (
            <div className="flex items-start gap-2 rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
              <AlertCircle size={15} className="mt-0.5 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {detailLoading ? (
            <div className="flex items-center justify-center py-16 text-sm text-gray-400">
              <Loader2 size={20} className="mr-2 animate-spin" />
              Loading block detail…
            </div>
          ) : view === 'yaml' ? (
            <div className="space-y-2">
              {yamlError && (
                <div className="flex items-start gap-2 rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
                  <AlertCircle size={15} className="mt-0.5 shrink-0" />
                  <span>{yamlError}</span>
                </div>
              )}
              <textarea
                value={yamlDraft ?? ''}
                onChange={(e) => {
                  setYamlDraft(e.target.value);
                  if (yamlError) setYamlError(null);
                }}
                spellCheck={false}
                placeholder={'api_version: blocks/v1\nkind: ' + kind + '\nname: My Block'}
                className="input-mono h-96 w-full resize-y text-xs leading-relaxed"
              />
              <p className="text-[11px] text-gray-400 dark:text-gray-500">
                Raw block YAML — parsed on save and when switching back to Form. Keep indentation consistent (2 spaces
                per level).
              </p>
            </div>
          ) : spec ? (
              <>
                {/* Common meta */}
                <Section title="Details">
                  <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <TextField
                      label="ID"
                      value={meta.id}
                      onChange={(v) => setMetaField('id', v)}
                      mono
                      disabled={mode === 'edit'}
                      hint={mode === 'edit' ? 'ID is fixed after creation' : 'lowercase letters, digits, _ or -'}
                      placeholder="my-block"
                    />
                    <TextField label="Name" value={meta.name} onChange={(v) => setMetaField('name', v)} placeholder="My Block" />
                    <TextField
                      label="Description"
                      value={meta.description}
                      onChange={(v) => setMetaField('description', v)}
                      className="md:col-span-2"
                      placeholder="What this block does…"
                    />
                    <TextField label="Version" value={meta.version} onChange={(v) => setMetaField('version', v)} placeholder="1.0.0" />
                    <TextField label="Author" value={meta.author} onChange={(v) => setMetaField('author', v)} placeholder="UnicoLab" />
                    <TextField label="License" value={meta.license} onChange={(v) => setMetaField('license', v)} placeholder="MIT" />
                    <TextField label="Language" value={meta.language} onChange={(v) => setMetaField('language', v)} placeholder="go, python, rust…" />
                    <TextField label="Icon (emoji)" value={meta.icon} onChange={(v) => setMetaField('icon', v)} placeholder="🐹" />
                    <CommaField label="Tags" value={meta.tags} onChange={(v) => setMetaField('tags', v)} placeholder="go, worker" />
                    <CheckboxField
                      label="Shareable — include in marketplace export"
                      checked={meta.shareable}
                      onChange={(v) => setMetaField('shareable', v)}
                      className="md:col-span-2"
                    />
                  </div>
                </Section>

                {kind === 'agent' && <AgentSpecEditor spec={spec as AgentBlockSpec} onChange={setSpec} />}
                {kind === 'quality' && <QualitySpecEditor spec={spec as QualityBlockSpec} onChange={setSpec} />}
                {kind === 'pack' && (
                  <PackSpecEditor spec={spec as PackBlockSpec} onChange={setSpec} refs={refs} agentOptions={agentOptions} />
                )}
                {kind === 'pipeline' && (
                  <PipelineSpecEditor spec={spec as PipelineConfig} onChange={setSpec} agentOptions={agentOptions} />
                )}
              </>
          ) : null}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 border-t border-gray-200 px-6 py-4 dark:border-gray-800">
          <button onClick={onClose} className="btn-ghost text-sm">Cancel</button>
          <button
            onClick={handleSave}
            disabled={saving || detailLoading || (view === 'yaml' && !!yamlError)}
            className="btn-primary gap-1.5 text-sm"
          >
            {saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
            {mode === 'create' ? 'Create' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ── Agent spec editor ──
function AgentSpecEditor({ spec, onChange }: { spec: AgentBlockSpec; onChange: (s: AgentBlockSpec) => void }) {
  return (
    <Section title="Agent Spec">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <TextField label="Title" value={spec.title ?? ''} onChange={(v) => onChange({ ...spec, title: v })} placeholder="My Agent" />
        <TextField label="Description" value={spec.description ?? ''} onChange={(v) => onChange({ ...spec, description: v })} placeholder="What this agent does…" />
        <div className="md:col-span-2">
          <TextArea
            label="System prompt"
            mono
            rows={9}
            value={spec.system_prompt ?? ''}
            onChange={(v) => onChange({ ...spec, system_prompt: v })}
            placeholder="You are a specialist agent…"
            hint="Base instructions. The orchestrator injects project context, matched skills, plan, and task details at runtime."
          />
        </div>
        <CommaField label="Skills" value={spec.skills ?? []} onChange={(v) => onChange({ ...spec, skills: v })} placeholder="react, typescript, testing" />
        <div />
        <NumberField label="Max iterations" value={spec.max_iter ?? 10} onChange={(v) => onChange({ ...spec, max_iter: v })} min={1} />
        <NumberField label="Temperature" value={spec.temperature ?? 0.2} onChange={(v) => onChange({ ...spec, temperature: v })} step={0.01} min={0} max={2} />
        <NumberField label="Max tokens" value={spec.max_tokens ?? 2048} onChange={(v) => onChange({ ...spec, max_tokens: v })} min={1} />
        <TextField label="Model (override)" mono value={spec.model ?? ''} onChange={(v) => onChange({ ...spec, model: v })} placeholder="empty = inherit stack/global" />
        <TextField label="Provider (override)" mono value={spec.provider ?? ''} onChange={(v) => onChange({ ...spec, provider: v })} placeholder="empty = inherit stack/global" />
        <TextField
          label="Endpoint (override)"
          mono
          className="md:col-span-2"
          value={spec.endpoint ?? ''}
          onChange={(v) => onChange({ ...spec, endpoint: v })}
          placeholder="empty = provider default / global endpoint"
        />
        <CheckboxField label="Enable built-in tools" checked={spec.tools !== false} onChange={(v) => onChange({ ...spec, tools: v })} />
      </div>
    </Section>
  );
}

// ── Quality spec editor ──
function QualitySpecEditor({ spec, onChange }: { spec: QualityBlockSpec; onChange: (s: QualityBlockSpec) => void }) {
  const setCmds = (key: 'format' | 'lint' | 'typecheck' | 'test' | 'build', rows: QualityCheckCmd[]) =>
    onChange({ ...spec, [key]: rows });
  return (
    <Section title="Quality Spec">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <CommaField
          label="Detect files"
          value={spec.detect?.files ?? []}
          onChange={(v) => onChange({ ...spec, detect: { ...(spec.detect || {}), files: v } })}
          placeholder="go.mod, Cargo.toml"
        />
        <CommaField
          label="Detect extensions"
          value={spec.detect?.extensions ?? []}
          onChange={(v) => onChange({ ...spec, detect: { ...(spec.detect || {}), extensions: v } })}
          placeholder=".go, .rs"
        />
        <NumberField
          label="Detect priority"
          value={spec.detect?.priority ?? 20}
          onChange={(v) => onChange({ ...spec, detect: { ...(spec.detect || {}), priority: v } })}
          min={0}
        />
      </div>
      <CmdListEditor label="Format commands" value={spec.format ?? []} onChange={(rows) => setCmds('format', rows)} />
      <CmdListEditor label="Lint commands" value={spec.lint ?? []} onChange={(rows) => setCmds('lint', rows)} />
      <CmdListEditor label="Typecheck commands" value={spec.typecheck ?? []} onChange={(rows) => setCmds('typecheck', rows)} />
      <CmdListEditor label="Test commands" value={spec.test ?? []} onChange={(rows) => setCmds('test', rows)} />
      <CmdListEditor label="Build commands" value={spec.build ?? []} onChange={(rows) => setCmds('build', rows)} />
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <TextField
          label="Smoke"
          mono
          value={spec.smoke ?? ''}
          onChange={(v) => onChange({ ...spec, smoke: v })}
          placeholder="go test ./... -count=1"
          hint="Quick verification after edits"
        />
        <TextField
          label="QA gate"
          mono
          value={spec.qa_gate ?? ''}
          onChange={(v) => onChange({ ...spec, qa_gate: v })}
          placeholder="go test ./..."
          hint="Gate command; falls back to smoke / first test"
        />
        <CommaField
          label="Safe prefixes"
          value={spec.safe_prefixes ?? []}
          onChange={(v) => onChange({ ...spec, safe_prefixes: v })}
          placeholder="go, git, npm"
          hint="Shell commands allowed without approval"
        />
        <TextArea
          label="Tester hints"
          rows={3}
          value={spec.tester_hints ?? ''}
          onChange={(v) => onChange({ ...spec, tester_hints: v })}
          placeholder="What the tester should focus on…"
        />
      </div>
    </Section>
  );
}

// ── Pack spec editor ──
function PackSpecEditor({ spec, onChange, refs, agentOptions }: {
  spec: PackBlockSpec;
  onChange: (s: PackBlockSpec) => void;
  refs: Refs;
  agentOptions: string[];
}) {
  const toggleAgent = (id: string, on: boolean) => {
    const cur = spec.agents ?? [];
    onChange({ ...spec, agents: on ? [...new Set([...cur, id])] : cur.filter((a) => a !== id) });
  };
  const blockLabel = (b: BlockCatalogEntry) => (b.name ? `${b.icon ?? ''} ${b.name}`.trim() : b.id);
  return (
    <Section title="Pack Spec">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <SelectField
          label="Pipeline"
          value={spec.pipeline ?? ''}
          onChange={(v) => onChange({ ...spec, pipeline: v })}
          options={refs.pipelines.map((b) => ({ value: b.id, label: blockLabel(b) }))}
          placeholder="— none —"
          hint="Pipeline block applied with this pack"
        />
        <SelectField
          label="Quality"
          value={spec.quality ?? ''}
          onChange={(v) => onChange({ ...spec, quality: v })}
          options={refs.quality.map((b) => ({ value: b.id, label: blockLabel(b) }))}
          placeholder="— none —"
          hint="Quality block applied with this pack"
        />
      </div>

      <div>
        <span className="label">Agent blocks to materialize</span>
        <div className="grid grid-cols-1 gap-1 md:grid-cols-2">
          {refs.agentBlocks.map((b) => (
            <label
              key={b.id}
              className="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800/50"
            >
              <input
                type="checkbox"
                checked={(spec.agents ?? []).includes(b.id)}
                onChange={(e) => toggleAgent(b.id, e.target.checked)}
                className="h-3.5 w-3.5 rounded border-gray-300 text-brand-600 focus:ring-brand-500 dark:border-gray-600 dark:bg-gray-800"
              />
              <span className="truncate">{blockLabel(b)}</span>
              <span className="ml-auto shrink-0 font-mono text-[10px] text-gray-400">{b.id}</span>
            </label>
          ))}
          {refs.agentBlocks.length === 0 && <p className="text-xs text-gray-400">No agent blocks available</p>}
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <CommaField
          label="Skills"
          value={spec.skills ?? []}
          onChange={(v) => onChange({ ...spec, skills: v })}
          placeholder="atomic-coding, react"
          hint="Injected as preferred skills"
        />
        <div className="flex items-end pb-1">
          <CheckboxField label="Pin skills to config on apply" checked={!!spec.pin_skills} onChange={(v) => onChange({ ...spec, pin_skills: v })} />
        </div>
        <SelectField
          label="Override tester"
          value={spec.override_tester ?? ''}
          onChange={(v) => onChange({ ...spec, override_tester: v })}
          options={agentOptions.map((a) => ({ value: a, label: a }))}
          placeholder="— none —"
          hint="Replaces the test phase agent"
        />
        <SelectField
          label="Override worker"
          value={spec.override_worker ?? ''}
          onChange={(v) => onChange({ ...spec, override_worker: v })}
          options={agentOptions.map((a) => ({ value: a, label: a }))}
          placeholder="— none —"
          hint="Replaces execute.default_role"
        />
        <CheckboxField label="Defer plan approval (force HITL ask)" checked={!!spec.defer_plan_approve} onChange={(v) => onChange({ ...spec, defer_plan_approve: v })} />
        <CheckboxField label="Defer clarify (force HITL ask)" checked={!!spec.defer_clarify} onChange={(v) => onChange({ ...spec, defer_clarify: v })} />
      </div>
    </Section>
  );
}

// ── Pipeline spec editor ──
function PipelineSpecEditor({ spec, onChange, agentOptions }: {
  spec: PipelineConfig;
  onChange: (s: PipelineConfig) => void;
  agentOptions: string[];
}) {
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [draftErr, setDraftErr] = useState<{ groupId: string; message: string } | null>(null);
  const [groupDraftErr, setGroupDraftErr] = useState<Record<number, string>>({});

  // Inline hint for group ids: warn (don't block) on empty / duplicate / invalid ids.
  // The backend rejects these on save; this just surfaces the problem while typing.
  const recomputeGroupErrs = (groups: GroupMeta[]) => {
    const errs: Record<number, string> = {};
    groups.forEach((g, gi) => {
      const gid = g.id.trim();
      if (gid === '') errs[gi] = 'Group id is required';
      else if (groups.some((og, oi) => oi !== gi && og.id.trim() === gid)) errs[gi] = 'Duplicate group id';
      else if (!BLOCK_ID_RE.test(gid)) errs[gi] = 'Invalid id (lowercase letters, digits, _ or -)';
    });
    setGroupDraftErr(errs);
  };

  const setPhase = (id: string, patch: Partial<PhaseSpec>) =>
    onChange({
      ...spec,
      phases: {
        ...spec.phases,
        [id]: { ...(spec.phases[id] ?? { agent: '', when: 'always', label: id, tip: '', group: '' }), ...patch },
      },
    });

  const removePhase = (id: string) => {
    const phases = { ...spec.phases };
    const cur = phases[id];
    if (cur) {
      // Archive instead of hard-delete: the backend Normalize() merges missing
      // default phase keys back in, so a deleted phase would resurrect.
      phases[id] = { ...cur, when: 'never', enabled: false, group: '' };
    }
    onChange({ ...spec, phases, groups: spec.groups.map((g) => ({ ...g, steps: (g.steps ?? []).filter((s) => s !== id) })) });
  };

  const restorePhase = (id: string) => {
    const cur = spec.phases[id];
    if (!cur) return;
    const targetGroup = spec.groups.find((g) => g.id === cur.group)?.id || spec.groups[0]?.id || '';
    onChange({
      ...spec,
      phases: { ...spec.phases, [id]: { ...cur, when: 'auto', enabled: true, group: targetGroup } },
      groups: spec.groups.map((g) =>
        g.id === targetGroup && !(g.steps ?? []).includes(id) ? { ...g, steps: [...(g.steps ?? []), id] } : g,
      ),
    });
  };

  const isArchived = useCallback(
    (id: string) => {
      const p = spec.phases[id];
      if (!p) return false;
      if (p.when === 'never' && p.enabled === false) {
        return !spec.groups.some((g) => (g.steps ?? []).includes(id));
      }
      return false;
    },
    [spec.phases, spec.groups],
  );

  const movePhase = (id: string, toGroup: string) => {
    const from = spec.phases[id]?.group ?? '';
    if (from === toGroup) return;
    onChange({
      ...spec,
      phases: { ...spec.phases, [id]: { ...spec.phases[id], group: toGroup } },
      groups: spec.groups.map((g) => {
        if (g.id === from) return { ...g, steps: (g.steps ?? []).filter((s) => s !== id) };
        if (g.id === toGroup) return { ...g, steps: [...(g.steps ?? []), id] };
        return g;
      }),
    });
  };

  const updateGroup = (index: number, patch: Partial<GroupMeta>) => {
    const groups = [...spec.groups];
    const old = groups[index];
    const newId = patch.id !== undefined && patch.id !== old.id ? patch.id : null;
    groups[index] = { ...old, ...patch, id: newId ?? old.id };
    let phases = spec.phases;
    if (newId !== null) {
      phases = Object.fromEntries(
        Object.entries(spec.phases ?? {}).map(([pid, ps]) => [pid, ps.group === old.id ? { ...ps, group: newId } : ps]),
      );
    }
    recomputeGroupErrs(groups);
    onChange({ ...spec, groups, phases });
  };

  const removeGroup = (index: number) => {
    const g = spec.groups[index];
    const groups = spec.groups.filter((_, i) => i !== index);
    recomputeGroupErrs(groups);
    onChange({
      ...spec,
      groups,
      phases: Object.fromEntries(
        Object.entries(spec.phases ?? {}).map(([pid, ps]) => [pid, ps.group === g.id ? { ...ps, group: '' } : ps]),
      ),
    });
  };

  const moveGroup = (index: number, dir: -1 | 1) => {
    const groups = [...spec.groups];
    const target = index + dir;
    if (target < 0 || target >= groups.length) return;
    [groups[index], groups[target]] = [groups[target], groups[index]];
    recomputeGroupErrs(groups);
    onChange({ ...spec, groups });
  };

  const addGroup = () => {
    let n = spec.groups.length + 1;
    let id = `group-${n}`;
    while (spec.groups.some((g) => g.id === id)) {
      n += 1;
      id = `group-${n}`;
    }
    const groups = [...spec.groups, { id, label: `Group ${n}`, steps: [] }];
    recomputeGroupErrs(groups);
    onChange({ ...spec, groups });
  };

  const addPhase = (groupId: string) => {
    const id = (draft[groupId] ?? '').trim().toLowerCase();
    const fail = (message: string) => setDraftErr({ groupId, message });
    if (!id) return fail('Enter a phase id first');
    if (!BLOCK_ID_RE.test(id)) return fail('Phase id must be lowercase letters, digits, _ or - (2–64 chars)');
    if (spec.phases[id]) return fail(`Phase "${id}" already exists`);
    onChange({
      ...spec,
      groups: spec.groups.map((g) => (g.id === groupId ? { ...g, steps: [...(g.steps ?? []), id] } : g)),
      phases: { ...spec.phases, [id]: { agent: '', when: 'always', label: id, tip: '', group: groupId, enabled: true } },
    });
    setDraft((d) => ({ ...d, [groupId]: '' }));
    setDraftErr(null);
  };

  const setExecute = (patch: Partial<ExecuteLoop>) => {
    const execute = spec.execute ?? { default_role: '', reviewer: '', corrector: '' };
    onChange({ ...spec, execute: { ...execute, ...patch } });
  };

  const updateSlot = (index: number, patch: Partial<Slot>) => {
    const slots = [...(spec.slots ?? [])];
    slots[index] = { ...slots[index], ...patch };
    onChange({ ...spec, slots });
  };

  const removeSlot = (index: number) => onChange({ ...spec, slots: (spec.slots ?? []).filter((_, i) => i !== index) });

  const addSlot = () => {
    const slots = spec.slots ?? [];
    let n = slots.length + 1;
    let id = `slot-${n}`;
    while (slots.some((s) => s.id === id)) {
      n += 1;
      id = `slot-${n}`;
    }
    onChange({
      ...spec,
      slots: [
        ...slots,
        {
          id,
          title: `Slot ${n}`,
          agent: spec.execute?.default_role ?? '',
          before: '',
          after: '',
          replace: '',
          when: 'always',
          input: '',
          persist_to: 'scratch',
          fail_mode: 'continue',
          enabled: true,
        },
      ],
    });
  };

  const grouped = useMemo(() => new Set(spec.groups.flatMap((g) => g.steps ?? [])), [spec.groups]);
  const orphanIds = useMemo(
    () => Object.keys(spec.phases ?? {}).filter((id) => !grouped.has(id) && !isArchived(id)),
    [spec.phases, grouped, isArchived],
  );
  const archivedIds = useMemo(
    () => Object.keys(spec.phases ?? {}).filter((id) => isArchived(id)),
    [spec.phases, isArchived],
  );

  const renderPhaseRow = (id: string, phase: PhaseSpec) => (
    <div key={id} className="space-y-2 rounded-lg border border-gray-200 bg-gray-50/60 p-3 dark:border-gray-800 dark:bg-gray-800/30">
      <div className="flex items-center gap-2">
        <span className="shrink-0 rounded bg-brand-50 px-2 py-1 font-mono text-[11px] font-semibold text-brand-600 dark:bg-brand-900/30 dark:text-brand-300">
          {id}
        </span>
        <input
          value={phase.label ?? ''}
          onChange={(e) => setPhase(id, { label: e.target.value })}
          className="input text-sm"
          placeholder="Phase label"
        />
        <button
          onClick={() => removePhase(id)}
          className="btn-ghost shrink-0 rounded-lg p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
          title="Remove phase"
        >
          <Trash2 size={13} />
        </button>
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <SuggestInput label="Agent" value={phase.agent ?? ''} onChange={(v) => setPhase(id, { agent: v })} suggestions={agentOptions} placeholder="agent id" />
        <SelectField label="When" value={phase.when || 'always'} onChange={(v) => setPhase(id, { when: v })} options={['always', 'auto', 'never']} />
        <TextField
          label="Tip"
          className="sm:col-span-2 lg:col-span-2"
          value={phase.tip ?? ''}
          onChange={(v) => setPhase(id, { tip: v })}
          placeholder="What this phase does"
        />
        <SelectField
          label="Group"
          value={phase.group ?? ''}
          onChange={(v) => movePhase(id, v)}
          options={[{ value: '', label: '(no group)' }, ...spec.groups.map((g) => ({ value: g.id, label: g.label || g.id }))]}
        />
        <CheckboxField label="Enabled" checked={phase.enabled !== false} onChange={(v) => setPhase(id, { enabled: v })} className="mt-5" />
      </div>
    </div>
  );

  return (
    <>
      {/* Execute loop */}
      <Section title="Execute Loop">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          <SuggestInput label="Default role" value={spec.execute?.default_role ?? ''} onChange={(v) => setExecute({ default_role: v })} suggestions={agentOptions} placeholder="worker" />
          <NumberField label="Max waves" value={spec.execute?.max_waves ?? 2} onChange={(v) => setExecute({ max_waves: v })} min={0} />
          <SuggestInput label="Reviewer" value={spec.execute?.reviewer ?? ''} onChange={(v) => setExecute({ reviewer: v })} suggestions={agentOptions} placeholder="reviewer" />
          <SuggestInput label="Corrector" value={spec.execute?.corrector ?? ''} onChange={(v) => setExecute({ corrector: v })} suggestions={agentOptions} placeholder="corrector" />
        </div>
      </Section>

      {/* Groups + phases */}
      <Section
        title="Groups & Phases"
        actions={
          <button onClick={addGroup} className="btn-ghost gap-1 text-xs">
            <Plus size={12} />
            Add group
          </button>
        }
      >
        <p className="text-[11px] text-gray-400 dark:text-gray-500">
          Phases are shown per group in execution order. The pipeline `order` is derived automatically from group
          steps + orphan phases.
        </p>
        <div className="space-y-3">
          {spec.groups.map((g, gi) => (
            <div key={gi} className="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-gray-800">
              <div className="flex items-center gap-2">
                <div className="flex flex-col gap-1">
                  <input
                    value={g.id}
                    onChange={(e) => updateGroup(gi, { id: e.target.value })}
                    className={clsx('input-mono w-32 text-xs', groupDraftErr[gi] && 'border-red-400')}
                    placeholder="group id"
                    title="Group id"
                  />
                  {groupDraftErr[gi] && (
                    <p className="flex items-center gap-1 text-[10px] font-medium text-red-500">
                      <AlertCircle size={11} className="shrink-0" />
                      {groupDraftErr[gi]}
                    </p>
                  )}
                </div>
                <input
                  value={g.label ?? ''}
                  onChange={(e) => updateGroup(gi, { label: e.target.value })}
                  className="input flex-1 text-sm"
                  placeholder="Group label"
                />
                <button
                  onClick={() => moveGroup(gi, -1)}
                  disabled={gi === 0}
                  className="btn-ghost rounded-lg p-1.5 disabled:opacity-40"
                  title="Move group up"
                >
                  <ChevronUp size={14} />
                </button>
                <button
                  onClick={() => moveGroup(gi, 1)}
                  disabled={gi === spec.groups.length - 1}
                  className="btn-ghost rounded-lg p-1.5 disabled:opacity-40"
                  title="Move group down"
                >
                  <ChevronDown size={14} />
                </button>
                <button
                  onClick={() => removeGroup(gi)}
                  className="btn-ghost rounded-lg p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                  title="Remove group"
                >
                  <Trash2 size={14} />
                </button>
              </div>

              <div>
                <span className="label">Steps</span>
                <input
                  value={(g.steps ?? []).join(', ')}
                  onChange={(e) => updateGroup(gi, { steps: splitComma(e.target.value) })}
                  className="input-mono text-xs"
                  placeholder="phase ids, comma separated"
                />
              </div>

              {(g.steps ?? []).map((pid) =>
                spec.phases[pid] ? (
                  renderPhaseRow(pid, spec.phases[pid])
                ) : (
                  <div
                    key={pid}
                    className="flex items-center gap-2 rounded-lg border border-amber-200 bg-amber-50/50 px-3 py-2 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/10 dark:text-amber-300"
                  >
                    <AlertCircle size={13} className="shrink-0" />
                    Phase "{pid}" is missing — it will be created with defaults on save.
                  </div>
                ),
              )}

              <div className="flex items-center gap-2">
                <input
                  value={draft[g.id] ?? ''}
                  onChange={(e) => setDraft((d) => ({ ...d, [g.id]: e.target.value }))}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      addPhase(g.id);
                    }
                  }}
                  className="input-mono flex-1 text-xs"
                  placeholder="new-phase-id"
                />
                <button onClick={() => addPhase(g.id)} className="btn-ghost shrink-0 gap-1 text-xs">
                  <Plus size={12} />
                  Add phase
                </button>
              </div>
              {draftErr && draftErr.groupId === g.id && (
                <p className="flex items-center gap-1.5 text-xs text-red-500">
                  <AlertCircle size={12} />
                  {draftErr.message}
                </p>
              )}
            </div>
          ))}
        </div>

        {orphanIds.length > 0 && (
          <div className="space-y-2">
            <span className="label">Phases not in any group</span>
            {orphanIds.map((pid) => renderPhaseRow(pid, spec.phases[pid]))}
          </div>
        )}

        {archivedIds.length > 0 && (
          <div className="space-y-2 rounded-lg border border-dashed border-gray-300 p-3 dark:border-gray-700">
            <div className="flex items-center gap-2">
              <Archive size={13} className="text-gray-400" />
              <span className="label mb-0">Archived (deleted) phases</span>
            </div>
            {archivedIds.map((pid) => (
              <div key={pid} className="flex items-center gap-2 rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-gray-800/40 dark:text-gray-400">
                <span className="font-mono line-through">{pid}</span>
                <span className="truncate opacity-70">{spec.phases[pid]?.agent ? `agent: ${spec.phases[pid]?.agent}` : 'no agent'}</span>
                <button
                  onClick={() => restorePhase(pid)}
                  className="btn-ghost ml-auto shrink-0 gap-1 rounded-lg px-2 py-1 text-emerald-600 dark:text-emerald-400"
                >
                  <RotateCcw size={12} />
                  Restore
                </button>
              </div>
            ))}
          </div>
        )}
      </Section>

      {/* Slots */}
      <Section
        title="Slots"
        actions={
          <button onClick={addSlot} className="btn-ghost gap-1 text-xs">
            <Plus size={12} />
            Add slot
          </button>
        }
      >
        {(spec.slots ?? []).length === 0 && (
          <p className="text-xs text-gray-400 dark:text-gray-500">
            No slots — agents run exactly at their phase anchors. Slots insert extra agent calls before, after, or
            instead of a phase.
          </p>
        )}
        {(spec.slots ?? []).map((slot, i) => (
          <div key={i} className="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-gray-800">
            <div className="flex items-center gap-2">
              <input
                value={slot.id}
                onChange={(e) => updateSlot(i, { id: e.target.value })}
                className="input-mono w-36 text-xs"
                placeholder="slot-id"
                title="Slot id"
              />
              <input
                value={slot.title ?? ''}
                onChange={(e) => updateSlot(i, { title: e.target.value })}
                className="input flex-1 text-sm"
                placeholder="Slot title"
              />
              <button
                onClick={() => removeSlot(i)}
                className="btn-ghost shrink-0 rounded-lg p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                title="Remove slot"
              >
                <Trash2 size={13} />
              </button>
            </div>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
              <SuggestInput label="Agent" value={slot.agent ?? ''} onChange={(v) => updateSlot(i, { agent: v })} suggestions={agentOptions} placeholder="agent id" />
              <TextField label="Before" mono value={slot.before ?? ''} onChange={(v) => updateSlot(i, { before: v })} placeholder="phase id" />
              <TextField label="After" mono value={slot.after ?? ''} onChange={(v) => updateSlot(i, { after: v })} placeholder="phase id" />
              <TextField label="Replace" mono value={slot.replace ?? ''} onChange={(v) => updateSlot(i, { replace: v })} placeholder="phase id" />
              <SuggestInput label="When" value={slot.when ?? 'always'} onChange={(v) => updateSlot(i, { when: v })} suggestions={['always', 'never', 'query_matches:']} placeholder="always" />
              <SelectField label="Persist to" value={slot.persist_to ?? 'scratch'} onChange={(v) => updateSlot(i, { persist_to: v })} options={['none', 'scratch', 'context', 'memory']} />
              <SelectField label="Fail mode" value={slot.fail_mode ?? 'continue'} onChange={(v) => updateSlot(i, { fail_mode: v })} options={['continue', 'abort']} />
              <CheckboxField label="Enabled" checked={slot.enabled !== false} onChange={(v) => updateSlot(i, { enabled: v })} className="mt-5" />
            </div>
            <TextArea
              label="Input template"
              rows={3}
              value={slot.input ?? ''}
              onChange={(v) => updateSlot(i, { input: v })}
              placeholder="Prompt template — supports {{query}}, {{exploration}}, {{plan}}, {{phase}}"
            />
          </div>
        ))}
      </Section>
    </>
  );
}
