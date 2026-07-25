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
  artifactKinds?: KindCount[];
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
}

// Diff excerpts (up to 16 KiB each) ride ONLY the per-call detail
// lookup, fetched when a row is expanded — never list responses.
export interface ToolCallDetail extends ToolCallRow {
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

// parseEnvelope narrows an unchecked JSON.parse result to the response
// envelope. JSON.parse returns `any`, so asserting the shape would be a
// promise the parser cannot keep — a proxy error page that happens to be
// valid JSON would sail through and fail later as a confusing undefined.
// The guard checks the one field the contract guarantees.
function parseEnvelope<T>(body: string): Envelope<T> | undefined {
  let parsed: Envelope<T>;
  try {
    parsed = JSON.parse(body);
  } catch {
    return undefined; // not JSON at all
  }
  // The declared type is a claim about the server, not a fact about these
  // bytes, so it is checked: `schema` is the one field every ccpeek
  // response carries.
  if (typeof parsed !== "object" || typeof parsed?.schema !== "string") {
    return undefined; // JSON, but not one of our envelopes
  }
  return parsed;
}

// unwrap reads the response body ONCE and tolerates it not being JSON.
// Parsing before checking the status meant any non-JSON reply — the
// API-only build's text/plain 501, a proxy error page, the Host guard's
// 403 — threw "Unexpected token < in JSON" instead of the real problem,
// and that string is what every page's error branch showed the user.
async function unwrap<T>(res: Response): Promise<T> {
  const body = await res.text();
  const env = parseEnvelope<T>(body);
  if (!res.ok) {
    const detail = env?.error ?? body.trim().split("\n")[0];
    throw new Error(
      detail
        ? `${res.status} ${res.statusText}: ${detail}`
        : `HTTP ${res.status}`,
    );
  }
  if (!env) {
    throw new Error(`${res.url}: expected JSON, got ${body.slice(0, 120)}`);
  }
  return env.data;
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
  return unwrap<T>(await fetch(`/api/v1${path}${qs}`));
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
  }) => get<SessionSummary[]>("/sessions", filters),

  session: (agent: string, id: string) =>
    get<SessionDetail>(`/sessions/${agent}/${id}`),

  transcript: (
    agent: string,
    id: string,
    opts?: { from?: string; limit?: string; full?: string },
  ) => get<TranscriptMessage[]>(`/sessions/${agent}/${id}/transcript`, opts),

  usage: (filters: {
    group?: string;
    agent?: string;
    model?: string;
    since?: string;
    until?: string;
    limit?: string;
  }) => get<UsageRow[]>("/usage", filters),

  search: (q: string, agent = "", limit = "20") =>
    get<SearchHit[]>("/search", { q, agent, limit }),

  stats: () => get<Stats>("/stats"),

  commands: (filters: {
    agent?: string;
    project?: string;
    q?: string;
    since?: string;
    until?: string;
    limit?: string;
    offset?: string;
  }) => get<CommandRow[]>("/commands", filters),

  sessionTools: (
    agent: string,
    id: string,
    opts?: {
      limit?: number;
      offset?: number;
      fromSeq?: number;
      toSeq?: number;
      compact?: boolean;
    },
  ) =>
    get<ToolCallRow[]>(`/sessions/${agent}/${id}/tools`, {
      limit: String(opts?.limit ?? 0),
      offset: String(opts?.offset ?? 0),
      from_seq: opts?.fromSeq ? String(opts.fromSeq) : "",
      to_seq: opts?.toSeq ? String(opts.toSeq) : "",
      compact: opts?.compact ? "1" : "",
    }),

  sessionToolDetail: (agent: string, id: string, seq: number) =>
    get<ToolCallDetail>(`/sessions/${agent}/${id}/tools/${seq}`),
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

export function fmtWhen(ts: string): string {
  if (!ts) return "";
  return ts.slice(0, 16).replace("T", " ");
}

export function shortPath(p: string): string {
  return p.replace(/^\/Users\/[^/]+/, "~").replace(/^\/home\/[^/]+/, "~");
}

/** clipPath shortens a path for a fixed-width cell by dropping LEADING
 *  segments, never trailing ones. Paths differentiate at the tail, so CSS
 *  `truncate` — which cuts the end — turns three sibling files into three
 *  identical "internal/handle…" rows. Left-clipping keeps the part that
 *  tells them apart. */
export function clipPath(p: string, max = 40): string {
  const short = shortPath(p);
  if (short.length <= max) return short;
  return "…" + short.slice(-(max - 1));
}

/** clipCommand keeps a shell command to one line for a list cell,
 *  shortening the home prefix so `cd /Users/me/…` reads like everywhere
 *  else in the UI. */
export function clipCommand(cmd: string, max = 60): string {
  const line = shortPath(cmd.split("\n")[0].trim());
  return line.length <= max ? line : line.slice(0, max - 1) + "…";
}

/** fmtCost renders money at a precision the figure can actually support.
 *  Every cost below $1 used to print four decimals, so the overwhelmingly
 *  common zero-cost row rendered as "$0.0000" — a column of false
 *  precision. Zero now reads as an em dash (there is no cost, not a very
 *  small one) and sub-cent amounts say so rather than pretending to
 *  hundredths-of-a-cent accuracy. */
export function fmtCost(usd: number, unpriced?: number): string {
  if (!(usd > 0)) return unpriced ? "—+" : "—";
  const cost =
    usd >= 1
      ? `$${usd.toFixed(2)}`
      : usd >= 0.01
        ? `$${usd.toFixed(3)}`
        : "<$0.01";
  return unpriced ? `${cost}+` : cost;
}

/** fmtCostExact is the full-precision figure for tooltips and titles,
 *  where the abbreviation in fmtCost would hide a real difference. */
export function fmtCostExact(usd: number): string {
  return `$${usd.toFixed(6).replace(/0+$/, "").replace(/\.$/, ".00")}`;
}

/** fmtTokens abbreviates TOKEN magnitudes, where a thousands suffix is
 *  the conventional and useful reading. */
export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

/** fmtCount formats a COUNT of entities — messages, commands, tool calls,
 *  sessions. These were going through fmtTokens, which rendered 1,413
 *  messages as "1.4k": an abbreviation built for six-figure token totals,
 *  applied to numbers a reader wants exactly. */
export function fmtCount(n: number): string {
  return n.toLocaleString();
}

/** plural picks the noun form for a count, so rows stop reading
 *  "1 edits". */
export function plural(n: number, one: string, many = one + "s"): string {
  return `${fmtCount(n)} ${n === 1 ? one : many}`;
}

export function fmtBytes(n: number): string {
  if (n >= 1_048_576) return `${(n / 1_048_576).toFixed(1)} MB`;
  if (n >= 1_024) return `${(n / 1_024).toFixed(1)} KB`;
  return `${n} B`;
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
  return unwrap<T>(
    await fetch(`/api/v1${path}`, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

export const parityApi = {
  artifacts: (kind?: string, agent?: string, limit = 100, offset = 0) =>
    get<ArtifactSummary[]>("/artifacts", {
      kind: kind ?? "",
      agent: agent ?? "",
      limit: String(limit),
      offset: String(offset),
    }),
  artifact: (agent: string, kind: string, name: string) =>
    get<ArtifactDetail>(
      `/artifacts/${agent}/${kind}/${encodeURIComponent(name)}`,
    ),
  scan: (includeIgnored: boolean) =>
    get<ScanFinding[]>("/scan", { ignored: includeIgnored ? "1" : "" }),
  scanIgnore: (id: number, ignored: boolean) =>
    send<{ ignored: boolean }>("POST", `/scan/${id}/ignore`, { ignored }),
  blocks: (limit = 24, agent = "") =>
    get<BlockRow[]>("/blocks", { limit: String(limit), agent }),
  budget: () => get<Budget>("/budget"),
  setBudget: (monthlyUSD: number) =>
    send<Budget>("PUT", "/budget", { monthlyUSD }),
};
