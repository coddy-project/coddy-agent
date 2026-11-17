/** Wire types for GET /coddy/sessions/{id}/changes and its detail route. */

export type ChangeStatus = "added" | "modified" | "deleted";

/** One file of the working copy that differs from HEAD, as git reports it. */
export interface ChangedFile {
  path: string;
  status: ChangeStatus;
  additions: number;
  deletions: number;
  /** SHA-256 of the status and both raw versions, used by Viewed. */
  digest?: string;
  /** No line diff exists; additions, deletions and patch are empty. */
  binary: boolean;
  /** The patch was cut at the server cap; the line counts still cover the file. */
  truncated: boolean;
  /** At least one side is a raster image the detail route can serve raw. */
  image?: boolean;
  beforeImage?: boolean;
  afterImage?: boolean;
  beforeSize?: number;
  afterSize?: number;
  /** Present only when the request asked for it (list) or always (detail). */
  patch?: string;
}

export interface SessionChangeTotals {
  files: number;
  additions: number;
  deletions: number;
}

export interface SessionChanges {
  sessionId: string;
  files: ChangedFile[];
  totals: SessionChangeTotals;
  /** "git" when the folder is inside a repository, "" when it is in none. */
  vcs?: string;
  /** New files not listed: past the server's cap, or too large to read. */
  skipped?: number;
}

/** A single file with its unified patch, from the detail route. */
export interface SessionChangeDetail extends ChangedFile {
  patch: string;
  before?: string;
  after?: string;
}

export const EMPTY_SESSION_CHANGES: SessionChanges = {
  sessionId: "",
  files: [],
  totals: { files: 0, additions: 0, deletions: 0 },
};
