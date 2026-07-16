import Database from "better-sqlite3";
import path from "path";
import fs from "fs";

// ── DB Path Discovery ─────────────────────────────────────────────

function findProjectRoot(): string {
  let dir = __dirname;
  while (dir !== "/") {
    if (fs.existsSync(path.join(dir, "go.mod"))) return dir;
    dir = path.dirname(dir);
  }
  throw new Error("Project root not found (no go.mod)");
}

const DB_PATH = path.join(findProjectRoot(), "tmp", "DB", "sfpg.db");

function getDB(): Database.Database {
  if (!fs.existsSync(DB_PATH)) {
    throw new Error(
      `DB not found at ${DB_PATH}. Is the dev server running? (air on port 8083)`,
    );
  }
  return new Database(DB_PATH, { readonly: true });
}

// ── Queries ───────────────────────────────────────────────────────

/**
 * Open a shared read-only DB connection for use in beforeAll.
 * Remember to call db.close() in afterAll.
 */
export function openDB(): Database.Database {
  return getDB();
}

export function getFirstFolderID(): number | null {
  const db = getDB();
  const row = db.prepare("SELECT id FROM folders ORDER BY id LIMIT 1").get() as
    { id: number } | undefined;
  db.close();
  return row?.id ?? null;
}

export function getFirstFileID(): number | null {
  const db = getDB();
  const row = db.prepare("SELECT id FROM files ORDER BY id LIMIT 1").get() as
    { id: number } | undefined;
  db.close();
  return row?.id ?? null;
}

export function getFirstSubfolderID(parentID: number): number | null {
  const db = getDB();
  const row = db
    .prepare("SELECT id FROM folders WHERE parent_id = ? ORDER BY id LIMIT 1")
    .get(parentID) as { id: number } | undefined;
  db.close();
  return row?.id ?? null;
}

export function getImageCountInFolder(folderID: number): number {
  const db = getDB();
  const row = db
    .prepare("SELECT COUNT(*) as count FROM files WHERE folder_id = ?")
    .get(folderID) as { count: number };
  db.close();
  return row.count;
}

export function getFileName(fileID: number): string | null {
  const db = getDB();
  const row = db
    .prepare("SELECT filename FROM files WHERE id = ?")
    .get(fileID) as { filename: string } | undefined;
  db.close();
  return row?.filename ?? null;
}

export function getFolderIDForFile(fileID: number): number | null {
  const db = getDB();
  const row = db
    .prepare("SELECT folder_id FROM files WHERE id = ?")
    .get(fileID) as { folder_id: number } | undefined;
  db.close();
  return row?.folder_id ?? null;
}

export function getFolderName(folderID: number): string | null {
  const db = getDB();
  const row = db
    .prepare("SELECT name FROM folders WHERE id = ?")
    .get(folderID) as { name: string } | undefined;
  db.close();
  return row?.name ?? null;
}

/** First folder containing at least `minCount` images (for lightbox nav tests). */
export function getFirstFolderWithImages(minCount: number): number | null {
  const db = getDB();
  const row = db
    .prepare(
      "SELECT folder_id FROM files GROUP BY folder_id HAVING COUNT(*) >= ? ORDER BY MIN(id) LIMIT 1",
    )
    .get(minCount) as { folder_id: number } | undefined;
  db.close();
  return row?.folder_id ?? null;
}

/** Ordered list of file IDs in a folder (for first/last-image assertions). */
export function getFileIDsInFolder(folderID: number): number[] {
  const db = getDB();
  const rows = db
    .prepare("SELECT id FROM files WHERE folder_id = ? ORDER BY id")
    .all(folderID) as Array<{ id: number }>;
  db.close();
  return rows.map((r) => r.id);
}
