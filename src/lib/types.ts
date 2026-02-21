// -- Index metadata --

export interface IndexData {
  generatedAt: string;
  plans: PlanEntry[];
  shellSnapshots: ShellSnapshotEntry[];
  todos: TodoEntry[];
  projects: ProjectEntry[];
  fileHistory: FileHistoryEntry[];
  history: HistoryEntry[];
}

export interface PlanEntry {
  fileName: string;
  title: string;
  sizeBytes: number;
}

export interface ShellSnapshotEntry {
  fileName: string;
  timestamp: number;
  sizeBytes: number;
}

export interface TodoEntry {
  fileName: string;
  itemCount: number;
  statuses: Record<string, number>;
}

export interface ProjectEntry {
  dirName: string;
  displayName: string;
  sessionCount: number;
  sessions: SessionEntry[];
}

export interface SessionEntry {
  sessionId: string;
  firstPrompt: string;
  messageCount: number;
  created: string;
  modified: string;
  gitBranch?: string;
  projectPath?: string;
}

export interface FileHistoryEntry {
  conversationId: string;
  fileCount: number;
}

export interface HistoryEntry {
  display: string;
  timestamp: number;
  project: string;
}

// -- Conversation messages --

export interface TextBlock {
  type: "text";
  text: string;
}

export interface ToolUseBlock {
  type: "tool_use";
  id?: string;
  name: string;
  input: Record<string, unknown>;
}

export interface ToolResultBlock {
  type: "tool_result";
  tool_use_id?: string;
  content: string | Array<{ type: string; text?: string }>;
}

export type ContentBlock = TextBlock | ToolUseBlock | ToolResultBlock;

export interface ConversationMessage {
  type: "user" | "assistant" | "system";
  timestamp: string;
  uuid: string;
  message: {
    role: string;
    content: string | ContentBlock[];
  };
  sessionId?: string;
  cwd?: string;
  gitBranch?: string;
}

// -- Todo items --

export interface TodoItem {
  content: string;
  status: string;
  activeForm?: string;
}

// -- File history detail --

export interface FileHistoryDetail {
  conversationId: string;
  files: FileVersionInfo[];
}

export interface FileVersionInfo {
  hash: string;
  version: number;
  content: string;
}
