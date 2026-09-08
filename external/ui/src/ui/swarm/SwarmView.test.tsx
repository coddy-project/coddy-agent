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
    const groups = [
      ...screen.getByTestId("swarm-groups").querySelectorAll(".swarm-group"),
    ];
    // One card per node the topology can reach, not only per node with work:
    // the card is the way in, and a node nobody has started on still needs one.
    expect(groups.map((g) => g.getAttribute("data-node")).sort()).toEqual([
      "middle",
      "middle/hidden",
      "nas02",
    ]);
    const nas02 = groups.find((g) => g.getAttribute("data-node") === "nas02");
    expect(nas02?.textContent).toContain("refactor the parser");
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

  it("offers a way into a node nobody has started work on", async () => {
    vi.stubGlobal("fetch", stubFetch({ sessions: [] }));
    const onOpen = vi.fn();
    render(<SwarmView onOpenNode={onOpen} />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-open-node-nas02")).toBeInTheDocument();
    });
    expect(screen.getByTestId("swarm-groups")).toHaveTextContent(
      "No sessions here yet",
    );
    // A relay in the chain is enterable too, and says what it is.
    expect(screen.getByTestId("swarm-groups")).toHaveTextContent(
      "A relay holds no sessions of its own",
    );
    fireEvent.click(screen.getByTestId("swarm-open-node-middle/hidden"));
    expect(onOpen).toHaveBeenCalledWith(["middle", "hidden"]);

    // A search asks about sessions, so it answers with sessions only.
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "nothing matches this" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-groups")).toHaveTextContent(
        "No sessions match",
      );
    });
  });

  // DESIGN.md fixes this and nothing covered it: clicking a node in the graph
  // filters the list to that node, and clicking it again clears the filter.
  it("filters the list from the graph, and clears it on a second click", async () => {
    render(<SwarmView />);
    await waitFor(() => {
      expect(
        screen.getByRole("img", { name: "Swarm topology" }),
      ).toBeInTheDocument();
    });
    const nodes = () =>
      [...screen.getByTestId("swarm-groups").querySelectorAll(".swarm-group")]
        .map((g) => g.getAttribute("data-node"))
        .sort();
    expect(nodes()).toEqual(["middle", "middle/hidden", "nas02"]);

    const hidden = [...document.querySelectorAll(".swarm-node")].find((n) =>
      (n.textContent || "").includes("hidden"),
    );
    expect(hidden).toBeTruthy();
    fireEvent.click(hidden!);
    await waitFor(() => {
      expect(nodes()).toEqual(["middle/hidden"]);
    });

    fireEvent.click(hidden!);
    await waitFor(() => {
      expect(nodes()).toEqual(["middle", "middle/hidden", "nas02"]);
    });
  });

  // The relay-as-home wiring lives in App.tsx and had no coverage at all. What
  // is testable here is the half SwarmView owns: with a header slot it renders
  // it, because on a relay that slot is the only environment control on screen.
  it("renders the header slot it is given", async () => {
    render(<SwarmView headerSlot={<button type="button">окружение</button>} />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-view")).toBeInTheDocument();
    });
    const slot = screen.getByRole("button", { name: "окружение" });
    const header = document.querySelector(".swarm-header-actions");
    expect(header?.contains(slot)).toBe(true);
    // Ahead of the topology toggle, which DESIGN.md fixes as the order.
    const toggle = document.querySelector(".swarm-graph-toggle");
    expect(
      slot.compareDocumentPosition(toggle!) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
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
