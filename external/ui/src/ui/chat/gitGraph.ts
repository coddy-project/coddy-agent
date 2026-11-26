export type GitCommit = {
  hash: string;
  parents: string[];
  subject: string;
  author: string;
  date: string;
  refs: string[];
};

export type GitHistoryPage = { commits: GitCommit[]; has_more: boolean };
type Lane = { hash: string; color: number };
export type GraphEdge = { from: number; to: number; color: number };
export type GraphRow = {
  lane: number;
  color: number;
  edges: GraphEdge[];
  width: number;
  incoming: boolean;
};

// Each pending parent owns a lane until its commit is reached. Reusing a parent
// joins its existing lane; a second parent opens another lane for a merge.
export function layoutGitGraph(commits: readonly GitCommit[]): GraphRow[] {
  let lanes: Lane[] = [];
  let nextColor = 0;
  return commits.map((commit) => {
    let lane = lanes.findIndex((entry) => entry.hash === commit.hash);
    const incoming = lane >= 0;
    if (lane < 0) {
      lane = lanes.length;
      lanes.push({ hash: commit.hash, color: nextColor++ });
    }
    const before = [...lanes];
    const color = before[lane]!.color;
    lanes.splice(lane, 1);
    commit.parents.forEach((hash, index) => {
      if (!lanes.some((entry) => entry.hash === hash)) {
        lanes.splice(Math.min(lane + index, lanes.length), 0, {
          hash,
          color: index === 0 ? color : nextColor++,
        });
      }
    });
    const edges: GraphEdge[] = [];
    before.forEach((entry, from) => {
      if (from === lane) return;
      const to = lanes.findIndex((next) => next.hash === entry.hash);
      if (to >= 0) edges.push({ from, to, color: entry.color });
    });
    commit.parents.forEach((hash) => {
      const to = lanes.findIndex((entry) => entry.hash === hash);
      edges.push({ from: lane, to, color: lanes[to]!.color });
    });
    return {
      lane,
      color,
      edges,
      width: Math.max(before.length, lanes.length),
      incoming,
    };
  });
}
