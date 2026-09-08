import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SwarmView } from "./SwarmView";

const info = {
  swarm: true,
  name: "outer",
  uuid: "root",
  version: "dev",
  node_count: 2,
  started_at: "2026-09-08T12:00:00Z",
  registry_warming: false,
};

const nodes = [
  {
    name: "nas02",
    kind: "agent",
    transport: "direct",
    url: "http://nas02:12345",
    instance_uuid: "u-nas02",
    online: true,
    generation: 1,
  },
  {
    name: "hidden",
    kind: "agent",
    transport: "tunnel",
    instance_uuid: "u-hidden",
    online: true,
    generation: 1,
  },
];

const sessions = [
  {
    id: "sess_alpha",
    title: "refactor the parser",
    updatedAt: "2026-09-08T12:05:00Z",
    cwd: "/srv/parser",
    agent_uuid: "u-nas02",
    node_path: ["nas02"],
    node_name: "nas02",
  },
  {
    id: "sess_alpha",
    title: "train the model",
    updatedAt: "2026-09-08T12:04:00Z",
    agent_uuid: "u-hidden",
    node_path: ["middle", "hidden"],
    node_name: "hidden",
  },
];

const topology = {
  root: { uuid: "root", name: "outer", kind: "relay", online: true },
  nodes: [
    { uuid: "u-mid", name: "middle", kind: "relay", online: true },
    { uuid: "u-nas02", name: "nas02", kind: "agent", online: true },
    { uuid: "u-hidden", name: "hidden", kind: "agent", online: true },
  ],
  edges: [
    { from_uuid: "root", to_uuid: "u-nas02", name: "nas02" },
    { from_uuid: "root", to_uuid: "u-mid", name: "middle" },
    { from_uuid: "u-mid", to_uuid: "u-hidden", name: "hidden" },
  ],
  routes: {
    "u-nas02": { path: ["nas02"] },
    "u-mid": { path: ["middle"] },
    "u-hidden": { path: ["middle", "hidden"] },
  },
  warnings: [],
};

let calls: string[] = [];

function stubFetch(
  overrides: { warnings?: string[]; sessions?: typeof sessions } = {},
) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    const body = (data: unknown) =>
      ({
        ok: true,
        status: 200,
        json: async () => data,
      }) as Response;
    if (url.startsWith("/swarm/info")) {
      return body(info);
    }
    if (url.startsWith("/swarm/nodes")) {
      return body({ nodes });
    }
    if (url.startsWith("/swarm/sessions")) {
      const q = new URL(url, "http://x").searchParams.get("q");
      const all = overrides.sessions ?? sessions;
      const rows = q ? all.filter((s) => (s.title || "").includes(q)) : all;
      return body({
        sessions: rows,
        warnings: overrides.warnings ?? [],
        node_more: {},
        hasMore: false,
      });
    }
    if (url.startsWith("/swarm/topology")) {
      return body(topology);
    }
    return { ok: false, status: 404, json: async () => ({}) } as Response;
  });
}

beforeEach(() => {
  calls = [];
  vi.stubGlobal("fetch", stubFetch());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("SwarmView", () => {
  it("groups sessions under the node that owns them", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-groups")).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    const groups = screen
      .getByTestId("swarm-groups")
      .querySelectorAll(".swarm-group");
    expect(groups).toHaveLength(2);
  });

  // The two rows share a session id and differ only by node, which is exactly
  // the case a bare id would collapse.
  it("keeps two nodes' identically named sessions apart", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    expect(screen.getByText("train the model")).toBeInTheDocument();
  });

  it("shows which route reaches a node several hops away", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByText("middle › hidden")).toBeInTheDocument();
    });
  });

  it("sends the search to the relay rather than filtering locally", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => {
      expect(
        calls.some(
          (c) => c.includes("/swarm/sessions") && c.includes("q=parser"),
        ),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(screen.queryByText("train the model")).not.toBeInTheDocument();
    });
  });

  it("filters to one node when its chip is picked", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByText("train the model")).toBeInTheDocument();
    });
    const chip = screen
      .getByTestId("swarm-chips")
      .querySelector<HTMLButtonElement>('button[title*="nas02"]');
    expect(chip).not.toBeNull();
    fireEvent.click(chip!);
    await waitFor(() => {
      expect(screen.queryByText("train the model")).not.toBeInTheDocument();
    });
    expect(screen.getByText("refactor the parser")).toBeInTheDocument();
  });

  it("draws the topology and lets it be hidden", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(
        screen.getByRole("img", { name: "Swarm topology" }),
      ).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "Hide topology" }));
    expect(
      screen.queryByRole("img", { name: "Swarm topology" }),
    ).not.toBeInTheDocument();
  });

  it("surfaces a node that could not be reached instead of hiding the rest", async () => {
    vi.stubGlobal("fetch", stubFetch({ warnings: ["gone: not reachable"] }));
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-warnings")).toHaveTextContent(
        "gone: not reachable",
      );
    });
    expect(screen.getByText("refactor the parser")).toBeInTheDocument();
  });

  it("says so when the environment is not a relay", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          ({ ok: false, status: 404, json: async () => ({}) }) as Response,
      ),
    );
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-view")).toHaveTextContent(
        "not a swarm relay",
      );
    });
  });

  it("asks for a token when the relay refuses everything but discovery", async () => {
    // /swarm/info is public, so the probe succeeds and the rest returns 401.
    // Reporting "no nodes" there would describe the swarm as empty when the
    // real problem is that this browser never got a credential.
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/swarm/info")) {
          return {
            ok: true,
            status: 200,
            json: async () => ({ swarm: true, name: "outer" }),
          } as Response;
        }
        return { ok: false, status: 401, json: async () => ({}) } as Response;
      }),
    );
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-error")).toHaveTextContent(
        "This relay needs a token",
      );
    });
    expect(screen.getByTestId("swarm-view")).not.toHaveTextContent(
      "No nodes have joined yet",
    );
  });

  it("separates an empty swarm from a filter that matched nothing", async () => {
    vi.stubGlobal("fetch", stubFetch({ sessions: [] }));
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-groups")).toHaveTextContent(
        "No sessions on these nodes yet",
      );
    });
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "nothing matches this" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-groups")).toHaveTextContent(
        "No sessions match",
      );
    });
  });

  it("hands a picked session to its owner", async () => {
    const onOpen = vi.fn();
    render(<SwarmView onOpenSession={onOpen} />);
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("refactor the parser"));
    expect(onOpen).toHaveBeenCalledTimes(1);
    const opened = onOpen.mock.calls[0]?.[0] as { node_path: string[] };
    expect(opened.node_path).toEqual(["nas02"]);
  });
});

describe("SwarmView actions", () => {
  it("offers a way into each node, carrying its route", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId("swarm-open-node-middle/hidden"));
    expect(onOpenNode).toHaveBeenCalledWith(["middle", "hidden"]);
  });

  it("hides the action when there is nowhere to send it", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    expect(
      screen.queryByTestId("swarm-open-node-nas02"),
    ).not.toBeInTheDocument();
  });
});
