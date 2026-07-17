// Typed client for /api/v1 — the same surface agents use (the SPA is its
// first client, keeping the agent-facing API honest by construction).

export interface TokenTotals {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export interface SessionSummary {
  agent: string;
  id: string;
  title: string;
  createdAt: string;
  modifiedAt: string;
  cwd: string;
  gitBranch?: string;
  messages: number;
  toolCalls: number;
  tokens: TokenTotals;
  costUSD: number;
  unpricedTokens?: number;
}

export interface Relation {
  kind: string;
  direction: "in" | "out";
  sessionId: string;
}

export interface LinkedArtifact {
  kind: string;
  name: string;
  relation: string;
  evidence: string;
}

export interface SessionDetail extends SessionSummary {
  relations?: Relation[];
  artifacts?: LinkedArtifact[];
  models?: string[];
}

export interface TranscriptMessage {
  seq: number;
  externalId?: string;
  parentId?: string;
  role: string;
  kind: string;
  createdAt: string;
  model?: string;
  isSidechain?: boolean;
  text: string;
  html?: string;
  content?: string;
}

export interface AgentStat {
  agent: string;
  sessions: number;
  lastActive?: string;
  tokens: number;
  costUSD: number;
}

export interface DayActivity {
  day: string;
  sessions: number;
  costUSD: number;
}

export interface WorkspaceStat {
  path: string;
  sessions: number;
  lastActive?: string;
}

export interface FileTouch {
  path: string;
  kind: string;
  agent: string;
  sessionId: string;
  at?: string;
}

export interface KindCount {
  kind: string;
  count: number;
}

export interface Stats {
  sessions: number;
  messages: number;
  toolCalls: number;
  commands: number;
  artifacts: number;
  scanFindings: number;
  tokens: number;
  costUSD: number;
  costMonthUSD: number;
  agents?: AgentStat[];
  activity?: DayActivity[];
  workspaces?: WorkspaceStat[];
  recentFiles?: FileTouch[];
  toolKinds?: KindCount[];
}

export interface CommandRow {
  command: string;
  at?: string;
  agent: string;
  sessionId: string;
  cwd?: string;
}

export interface ToolCallRow {
  seq: number;
  messageSeq: number;
  name: string;
  kind: string;
  detail?: string;
  status?: string;
  at?: string;
  old?: string;
  new?: string;
}

export interface UsageRow {
  group: string;
  sessions: number;
  messages: number;
  tokens: TokenTotals;
  costUSD: number;
  costReportedUSD: number;
  costEstimatedUSD: number;
  hasUnpriced?: boolean;
}

export interface SearchHit {
  docType: string;
  agent: string;
  sessionId?: string;
  seq?: number;
  artifact?: string;
  title?: string;
  snippet: string;
}

interface Envelope<T> {
  schema: string;
  data: T;
  error?: string;
}

async function get<T>(
  path: string,
  params?: Record<string, string | undefined>,
): Promise<T> {
  const entries = Object.entries(params ?? {}).filter(
    (e): e is [string, string] => e[1] != null && e[1] !== "",
  );
  const qs = entries.length
    ? "?" + new URLSearchParams(entries).toString()
    : "";
  const res = await fetch(`/api/v1${path}${qs}`);
  const env: Envelope<T> = await res.json();
  if (!res.ok) {
    throw new Error(env.error ?? `HTTP ${res.status}`);
  }
  return env.data;
}

export const api = {
  sessions: (filters: {
    agent?: string;
    project?: string;
    model?: string;
    q?: string;
    since?: string;
    until?: string;
    limit?: string;
    offset?: string;
  }) => get<SessionSummary[] | null>("/sessions", filters),

  session: (agent: string, id: string) =>
    get<SessionDetail>(`/sessions/${agent}/${id}`),

  transcript: (
    agent: string,
    id: string,
    opts?: { from?: string; limit?: string; full?: string },
  ) =>
    get<TranscriptMessage[] | null>(
      `/sessions/${agent}/${id}/transcript`,
      opts,
    ),

  usage: (filters: {
    group?: string;
    agent?: string;
    model?: string;
    since?: string;
    until?: string;
  }) => get<UsageRow[] | null>("/usage", filters),

  search: (q: string, limit = "20") =>
    get<SearchHit[] | null>("/search", { q, limit }),

  stats: () => get<Stats>("/stats"),

  commands: (filters: {
    agent?: string;
    project?: string;
    q?: string;
    since?: string;
    until?: string;
    limit?: string;
    offset?: string;
  }) => get<CommandRow[] | null>("/commands", filters),

  sessionTools: (agent: string, id: string) =>
    get<ToolCallRow[] | null>(`/sessions/${agent}/${id}/tools`),
};

// The validated per-agent palette (also defined as CSS vars in
// styles.css) — color follows the agent everywhere in the UI.
export const AGENT_COLOR: Record<string, string> = {
  "claude-code": "var(--color-agent-claude)",
  pi: "var(--color-agent-pi)",
  codex: "var(--color-agent-codex)",
  opencode: "var(--color-agent-opencode)",
  cursor: "var(--color-agent-cursor)",
};

// inclusiveUntil converts a date-picker "to" (inclusive) into the API's
// exclusive upper bound (next day).
export function inclusiveUntil(until: string): string {
  if (!until) return "";
  const d = new Date(until + "T00:00:00Z");
  if (Number.isNaN(d.getTime())) return until;
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
}

export function fmtWhen(ts: string): string {
  if (!ts) return "";
  return ts.slice(0, 16).replace("T", " ");
}

export function shortPath(p: string): string {
  return p.replace(/^\/Users\/[^/]+/, "~").replace(/^\/home\/[^/]+/, "~");
}

export function fmtCost(usd: number, unpriced?: number): string {
  const cost = usd >= 1 ? `$${usd.toFixed(2)}` : `$${usd.toFixed(4)}`;
  return unpriced ? `${cost}+` : cost;
}

export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

export function totalTokens(t: TokenTotals): number {
  return t.input + t.output + t.cacheRead + t.cacheWrite;
}

export interface ArtifactSummary {
  agent: string;
  kind: string;
  name: string;
  size: number;
  sessions: number;
}

export interface ArtifactDetail extends ArtifactSummary {
  content?: string;
  contentHTML?: string;
  metadata?: string;
  sessionIds?: string[];
  // Per-session transcript seq of the tool call that produced this artifact
  // (todo → TodoWrite, plan → ExitPlanMode), when one was found.
  sessionAnchors?: Record<string, number>;
}

export interface ScanFinding {
  id: number;
  ruleId: string;
  description: string;
  entityType: string;
  naturalKey: string;
  matchRedacted: string;
  line: number;
  scannedAt: string;
  ignored: boolean;
}

export interface BlockRow {
  start: string;
  end: string;
  sessions: number;
  messages: number;
  tokens: TokenTotals;
  costUSD: number;
  unpricedTokens?: number;
  active?: boolean;
}

export interface Budget {
  monthlyUSD: number;
  spentUSD: number;
  month: string;
}

async function send<T>(
  method: string,
  path: string,
  body: unknown,
): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const env: Envelope<T> = await res.json();
  if (!res.ok) throw new Error(env.error ?? `HTTP ${res.status}`);
  return env.data;
}

export const parityApi = {
  artifacts: (kind?: string, agent?: string) =>
    get<ArtifactSummary[] | null>("/artifacts", {
      kind: kind ?? "",
      agent: agent ?? "",
      limit: "500",
    }),
  artifact: (agent: string, kind: string, name: string) =>
    get<ArtifactDetail>(
      `/artifacts/${agent}/${kind}/${encodeURIComponent(name)}`,
    ),
  scan: (includeIgnored: boolean) =>
    get<ScanFinding[] | null>("/scan", { ignored: includeIgnored ? "1" : "" }),
  scanIgnore: (id: number, ignored: boolean) =>
    send<{ ignored: boolean }>("POST", `/scan/${id}/ignore`, { ignored }),
  blocks: (limit = 24) =>
    get<BlockRow[] | null>("/blocks", { limit: String(limit) }),
  budget: () => get<Budget>("/budget"),
  setBudget: (monthlyUSD: number) =>
    send<Budget>("PUT", "/budget", { monthlyUSD }),
};
