export type SessionRow = {
  id: string;
  title?: string;
  updatedAt?: string;
  cwd?: string;
  /** Flat labels the session is filed under; normalized by the server. */
  tags?: string[];
  /** True while the session sits in the archive rather than the working list. */
  archived?: boolean;
  archivedAt?: string;
  /** True while the session is held at the top of every listing. */
  pinned?: boolean;
  pinnedAt?: string;
  turnActive?: boolean;
  activitySeq?: number;
  readActivitySeq?: number;
  unreadComplete?: boolean;
  permissionPending?: boolean;
  /**
   * Background tasks of this session that are still in flight, as the server
   * counts them: the model's own work only, nothing finished. Detached work
   * outlives the turn that started it, so this can be above zero while
   * `turnActive` is false.
   */
  backgroundRunning?: number;
};
