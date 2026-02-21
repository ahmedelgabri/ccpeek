import fs from "node:fs";
import path from "node:path";
import type {
  IndexData,
  PlanEntry,
  ShellSnapshotEntry,
  TodoEntry,
  ProjectEntry,
  SessionEntry,
  FileHistoryEntry,
  FileHistoryDetail,
  FileVersionInfo,
  HistoryEntry,
  ConversationMessage,
  TodoItem,
} from "../src/lib/types.ts";

const CLAUDE_DIR = path.join(process.env.HOME!, ".claude");
const OUTPUT_DIR = path.join(import.meta.dirname, "..", "src", "data");

function ensureDir(dir: string) {
  fs.mkdirSync(dir, { recursive: true });
}

function readJsonl(filePath: string): unknown[] {
  const content = fs.readFileSync(filePath, "utf-8");
  return content
    .split("\n")
    .filter((line) => line.trim())
    .map((line) => {
      try {
        return JSON.parse(line);
      } catch {
        return null;
      }
    })
    .filter(Boolean);
}

// -- Plans --

function indexPlans(): PlanEntry[] {
  const dir = path.join(CLAUDE_DIR, "plans");
  if (!fs.existsSync(dir)) return [];

  const files = fs.readdirSync(dir).filter((f) => f.endsWith(".md"));
  const outDir = path.join(OUTPUT_DIR, "plans");
  ensureDir(outDir);

  const entries: PlanEntry[] = [];
  for (const fileName of files) {
    const src = path.join(dir, fileName);
    const stat = fs.statSync(src);
    const content = fs.readFileSync(src, "utf-8");

    // Extract title from first heading or use filename
    const titleMatch = content.match(/^#\s+(.+)/m);
    const title = titleMatch ? titleMatch[1] : fileName.replace(".md", "");

    fs.copyFileSync(src, path.join(outDir, fileName));
    entries.push({ fileName, title, sizeBytes: stat.size });
  }

  return entries;
}

// -- Shell Snapshots --

function indexShellSnapshots(): ShellSnapshotEntry[] {
  const dir = path.join(CLAUDE_DIR, "shell-snapshots");
  if (!fs.existsSync(dir)) return [];

  const files = fs.readdirSync(dir).filter((f) => f.endsWith(".sh"));
  const outDir = path.join(OUTPUT_DIR, "shell-snapshots");
  ensureDir(outDir);

  const entries: ShellSnapshotEntry[] = [];
  for (const fileName of files) {
    const src = path.join(dir, fileName);
    const stat = fs.statSync(src);

    // Extract timestamp from filename: snapshot-zsh-<TIMESTAMP>-<ID>.sh
    const tsMatch = fileName.match(/snapshot-\w+-(\d+)-/);
    const timestamp = tsMatch ? parseInt(tsMatch[1], 10) : stat.mtimeMs;

    fs.copyFileSync(src, path.join(outDir, fileName));
    entries.push({ fileName, timestamp, sizeBytes: stat.size });
  }

  entries.sort((a, b) => b.timestamp - a.timestamp);
  return entries;
}

// -- Todos --

function indexTodos(): TodoEntry[] {
  const dir = path.join(CLAUDE_DIR, "todos");
  if (!fs.existsSync(dir)) return [];

  const files = fs.readdirSync(dir).filter((f) => f.endsWith(".json"));
  const outDir = path.join(OUTPUT_DIR, "todos");
  ensureDir(outDir);

  const entries: TodoEntry[] = [];
  for (const fileName of files) {
    const src = path.join(dir, fileName);
    const content = fs.readFileSync(src, "utf-8");
    let items: TodoItem[];
    try {
      items = JSON.parse(content);
    } catch {
      continue;
    }

    if (!Array.isArray(items) || items.length === 0) continue;

    fs.copyFileSync(src, path.join(outDir, fileName));

    const statuses: Record<string, number> = {};
    for (const item of items) {
      statuses[item.status] = (statuses[item.status] || 0) + 1;
    }

    entries.push({ fileName, itemCount: items.length, statuses });
  }

  return entries;
}

// -- Projects --

function indexProjects(): ProjectEntry[] {
  const dir = path.join(CLAUDE_DIR, "projects");
  if (!fs.existsSync(dir)) return [];

  const projectDirs = fs
    .readdirSync(dir)
    .filter((d) => fs.statSync(path.join(dir, d)).isDirectory());

  const entries: ProjectEntry[] = [];

  for (const dirName of projectDirs) {
    const projectDir = path.join(dir, dirName);
    const outDir = path.join(OUTPUT_DIR, "projects", dirName);
    ensureDir(outDir);

    // Try to read sessions-index.json for metadata
    const sessionsIndexPath = path.join(projectDir, "sessions-index.json");
    let sessionsIndex: { entries: SessionEntry[] } | null = null;
    if (fs.existsSync(sessionsIndexPath)) {
      try {
        sessionsIndex = JSON.parse(
          fs.readFileSync(sessionsIndexPath, "utf-8"),
        );
      } catch {
        // ignore parse errors
      }
    }

    const jsonlFiles = fs
      .readdirSync(projectDir)
      .filter((f) => f.endsWith(".jsonl"));
    const sessions: SessionEntry[] = [];

    for (const jsonlFile of jsonlFiles) {
      const sessionId = jsonlFile.replace(".jsonl", "");
      const jsonlPath = path.join(projectDir, jsonlFile);

      // Parse JSONL and filter to conversation messages
      const lines = readJsonl(jsonlPath);
      const messages: ConversationMessage[] = [];

      for (const line of lines) {
        const obj = line as Record<string, unknown>;
        if (
          obj.type === "user" ||
          obj.type === "assistant" ||
          obj.type === "system"
        ) {
          // Only keep conversation-relevant fields
          messages.push({
            type: obj.type as ConversationMessage["type"],
            timestamp: obj.timestamp as string,
            uuid: obj.uuid as string,
            message: obj.message as ConversationMessage["message"],
            sessionId: obj.sessionId as string | undefined,
            cwd: obj.cwd as string | undefined,
            gitBranch: obj.gitBranch as string | undefined,
          });
        }
      }

      // Write parsed messages as JSON
      fs.writeFileSync(
        path.join(outDir, `${sessionId}.json`),
        JSON.stringify(messages),
      );

      // Build session metadata
      const indexEntry = sessionsIndex?.entries?.find(
        (e) => e.sessionId === sessionId,
      );

      if (indexEntry) {
        sessions.push({
          sessionId,
          firstPrompt: indexEntry.firstPrompt || "",
          messageCount: indexEntry.messageCount || messages.length,
          created: indexEntry.created || "",
          modified: indexEntry.modified || "",
          gitBranch: indexEntry.gitBranch,
          projectPath: indexEntry.projectPath,
        });
      } else {
        // Derive metadata from messages
        const firstUser = messages.find((m) => m.type === "user");
        const firstPrompt =
          typeof firstUser?.message?.content === "string"
            ? firstUser.message.content.slice(0, 200)
            : "";
        sessions.push({
          sessionId,
          firstPrompt,
          messageCount: messages.length,
          created: (firstUser?.timestamp as string) || "",
          modified: (messages[messages.length - 1]?.timestamp as string) || "",
          gitBranch: firstUser?.gitBranch,
        });
      }
    }

    sessions.sort(
      (a, b) =>
        new Date(b.modified || 0).getTime() -
        new Date(a.modified || 0).getTime(),
    );

    // Decode display name from dir name
    let displayName = dirName.startsWith("-") ? "/" + dirName.slice(1) : dirName;
    displayName = displayName.replace(/--/g, "/.").replace(/-/g, "/");

    entries.push({
      dirName,
      displayName,
      sessionCount: sessions.length,
      sessions,
    });
  }

  entries.sort((a, b) => b.sessionCount - a.sessionCount);
  return entries;
}

// -- File History --

function indexFileHistory(): FileHistoryEntry[] {
  const dir = path.join(CLAUDE_DIR, "file-history");
  if (!fs.existsSync(dir)) return [];

  const conversationDirs = fs
    .readdirSync(dir)
    .filter((d) => fs.statSync(path.join(dir, d)).isDirectory());

  const outDir = path.join(OUTPUT_DIR, "file-history");
  ensureDir(outDir);

  const entries: FileHistoryEntry[] = [];

  for (const conversationId of conversationDirs) {
    const convDir = path.join(dir, conversationId);
    const fileNames = fs.readdirSync(convDir);

    const files: FileVersionInfo[] = [];
    for (const fileName of fileNames) {
      const filePath = path.join(convDir, fileName);
      const stat = fs.statSync(filePath);
      if (!stat.isFile()) continue;

      // Parse hash@vN format
      const match = fileName.match(/^(.+)@v(\d+)$/);
      if (!match) continue;

      const content = fs.readFileSync(filePath, "utf-8");
      files.push({
        hash: match[1],
        version: parseInt(match[2], 10),
        content,
      });
    }

    files.sort((a, b) => a.hash.localeCompare(b.hash) || a.version - b.version);

    const detail: FileHistoryDetail = { conversationId, files };
    fs.writeFileSync(
      path.join(outDir, `${conversationId}.json`),
      JSON.stringify(detail),
    );

    entries.push({ conversationId, fileCount: files.length });
  }

  entries.sort((a, b) => b.fileCount - a.fileCount);
  return entries;
}

// -- History --

function indexHistory(): HistoryEntry[] {
  const historyPath = path.join(CLAUDE_DIR, "history.jsonl");
  if (!fs.existsSync(historyPath)) return [];

  const lines = readJsonl(historyPath);
  return (lines as HistoryEntry[]).sort((a, b) => b.timestamp - a.timestamp);
}

// -- Main --

function main() {
  console.log("Building index from", CLAUDE_DIR);
  console.log("Output directory:", OUTPUT_DIR);

  // Clean output dir
  if (fs.existsSync(OUTPUT_DIR)) {
    fs.rmSync(OUTPUT_DIR, { recursive: true });
  }
  ensureDir(OUTPUT_DIR);

  const plans = indexPlans();
  console.log(`  Plans: ${plans.length}`);

  const shellSnapshots = indexShellSnapshots();
  console.log(`  Shell snapshots: ${shellSnapshots.length}`);

  const todos = indexTodos();
  console.log(`  Todos: ${todos.length} (non-empty)`);

  const projects = indexProjects();
  const totalSessions = projects.reduce((s, p) => s + p.sessionCount, 0);
  console.log(`  Projects: ${projects.length} (${totalSessions} sessions)`);

  const fileHistory = indexFileHistory();
  console.log(`  File history: ${fileHistory.length} conversations`);

  const history = indexHistory();
  console.log(`  History: ${history.length} entries`);

  const index: IndexData = {
    generatedAt: new Date().toISOString(),
    plans,
    shellSnapshots,
    todos,
    projects,
    fileHistory,
    history,
  };

  fs.writeFileSync(
    path.join(OUTPUT_DIR, "index.json"),
    JSON.stringify(index, null, 2),
  );

  console.log("Done.");
}

main();
