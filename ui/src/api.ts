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
  role: string;
  kind: string;
  createdAt: string;
  model?: string;
  isSidechain?: boolean;
  text: string;
  content?: string;
}

export interface UsageRow {
  group: string;
  sessions: number;
  messages: number;
  tokens: TokenTotals;
  costUSD: number;
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

async function get<T>(path: string, params?: Record<string, string>): Promise<T> {
  const qs = params
    ? "?" +
      new URLSearchParams(
        Object.entries(params).filter(([, v]) => v !== ""),
      ).toString()
    : "";
  const res = await fetch(`/api/v1${path}${qs}`);
  const env = (await res.json()) as Envelope<T>;
  if (!res.ok) {
    throw new Error(env.error ?? `HTTP ${res.status}`);
  }
  return env.data;
}

export const api = {
  sessions: (filters: {
    agent?: string;
    project?: string;
    q?: string;
    since?: string;
    until?: string;
    limit?: string;
    offset?: string;
  }) => get<SessionSummary[] | null>("/sessions", filters as Record<string, string>),

  session: (agent: string, id: string) =>
    get<SessionDetail>(`/sessions/${agent}/${id}`),

  transcript: (agent: string, id: string, opts?: { from?: string; limit?: string; full?: string }) =>
    get<TranscriptMessage[] | null>(
      `/sessions/${agent}/${id}/transcript`,
      opts as Record<string, string>,
    ),

  usage: (filters: { group?: string; agent?: string; since?: string; until?: string }) =>
    get<UsageRow[] | null>("/usage", filters as Record<string, string>),

  search: (q: string, limit = "20") =>
    get<SearchHit[] | null>("/search", { q, limit }),
};

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
