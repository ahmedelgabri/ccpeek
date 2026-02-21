import { describe, it, expect, beforeAll } from "vitest";
import fs from "node:fs";
import path from "node:path";
import type { IndexData, FileHistoryDetail, TodoItem } from "../../../src/lib/types";

const DATA_DIR = path.join(process.cwd(), "src", "data");

// These tests validate the indexer output. They require `pnpm run index`
// to have been run first.
describe("build-index output", () => {
  let index: IndexData;

  beforeAll(() => {
    const raw = fs.readFileSync(path.join(DATA_DIR, "index.json"), "utf-8");
    index = JSON.parse(raw);
  });

  it("generates a valid index.json with all top-level fields", () => {
    expect(index.generatedAt).toBeTruthy();
    expect(Array.isArray(index.plans)).toBe(true);
    expect(Array.isArray(index.shellSnapshots)).toBe(true);
    expect(Array.isArray(index.todos)).toBe(true);
    expect(Array.isArray(index.projects)).toBe(true);
    expect(Array.isArray(index.fileHistory)).toBe(true);
    expect(Array.isArray(index.history)).toBe(true);
  });

  it("indexes plans with required fields", () => {
    expect(index.plans.length).toBeGreaterThan(0);
    for (const plan of index.plans) {
      expect(plan.fileName).toBeTruthy();
      expect(plan.title).toBeTruthy();
      expect(plan.sizeBytes).toBeGreaterThan(0);
    }
  });

  it("copies plan files to data directory", () => {
    for (const plan of index.plans) {
      const filePath = path.join(DATA_DIR, "plans", plan.fileName);
      expect(fs.existsSync(filePath)).toBe(true);
    }
  });

  it("indexes shell snapshots with timestamps", () => {
    expect(index.shellSnapshots.length).toBeGreaterThan(0);
    for (const snap of index.shellSnapshots) {
      expect(snap.fileName).toBeTruthy();
      expect(snap.timestamp).toBeGreaterThan(0);
    }
  });

  it("sorts shell snapshots newest first", () => {
    for (let i = 1; i < index.shellSnapshots.length; i++) {
      expect(index.shellSnapshots[i - 1].timestamp).toBeGreaterThanOrEqual(
        index.shellSnapshots[i].timestamp,
      );
    }
  });

  it("indexes only non-empty todos", () => {
    for (const todo of index.todos) {
      expect(todo.itemCount).toBeGreaterThan(0);
      const filePath = path.join(DATA_DIR, "todos", todo.fileName);
      const items: TodoItem[] = JSON.parse(fs.readFileSync(filePath, "utf-8"));
      expect(items.length).toBe(todo.itemCount);
    }
  });

  it("indexes projects with session data", () => {
    expect(index.projects.length).toBeGreaterThan(0);
    for (const project of index.projects) {
      expect(project.dirName).toBeTruthy();
      expect(project.displayName).toBeTruthy();
      expect(project.sessionCount).toBe(project.sessions.length);
    }
  });

  it("generates valid JSON files for project sessions", () => {
    // Check first project's first session
    const project = index.projects[0];
    const session = project.sessions[0];
    const filePath = path.join(
      DATA_DIR,
      "projects",
      project.dirName,
      `${session.sessionId}.json`,
    );
    expect(fs.existsSync(filePath)).toBe(true);
    const messages = JSON.parse(fs.readFileSync(filePath, "utf-8"));
    expect(Array.isArray(messages)).toBe(true);
  });

  it("filters out non-conversation message types from sessions", () => {
    const project = index.projects[0];
    const session = project.sessions[0];
    const filePath = path.join(
      DATA_DIR,
      "projects",
      project.dirName,
      `${session.sessionId}.json`,
    );
    const messages = JSON.parse(fs.readFileSync(filePath, "utf-8"));
    for (const msg of messages) {
      expect(["user", "assistant", "system"]).toContain(msg.type);
    }
  });

  it("indexes file history conversations", () => {
    expect(index.fileHistory.length).toBeGreaterThan(0);
    // Check first entry has valid JSON
    const entry = index.fileHistory[0];
    const filePath = path.join(
      DATA_DIR,
      "file-history",
      `${entry.conversationId}.json`,
    );
    expect(fs.existsSync(filePath)).toBe(true);
    const detail: FileHistoryDetail = JSON.parse(
      fs.readFileSync(filePath, "utf-8"),
    );
    expect(detail.conversationId).toBe(entry.conversationId);
    expect(detail.files.length).toBe(entry.fileCount);
  });

  it("indexes history entries sorted newest first", () => {
    expect(index.history.length).toBeGreaterThan(0);
    for (let i = 1; i < index.history.length; i++) {
      expect(index.history[i - 1].timestamp).toBeGreaterThanOrEqual(
        index.history[i].timestamp,
      );
    }
  });
});
