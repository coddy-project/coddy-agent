export type SessionRow = {
  id: string;
  title?: string;
  updatedAt?: string;
  cwd?: string;
  turnActive?: boolean;
  activitySeq?: number;
  readActivitySeq?: number;
  unreadComplete?: boolean;
  permissionPending?: boolean;
};
