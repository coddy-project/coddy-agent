import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
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
    turnActive: true,
  },
  {
    id: "sess_alpha",
    title: "train the model",
    updatedAt: "2026-09-08T12:04:00Z",
    agent_uuid: "u-hidden",
    node_path: ["middle", "hidden"],
    node_name: "hidden",
    permissionPending: true,
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

/** The <g> drawn for one node of the map, found by the name printed on it. */
function mapNode(name: string): Element {
  const found = [...document.querySelectorAll(".swarm-node")].find((n) =>
    (n.textContent || "").includes(name),
  );
  expect(found, `no node drawn for ${name}`).toBeTruthy();
  return found as Element;
}

async function drawn(): Promise<void> {
  await waitFor(() => {
    expect(
      screen.getByRole("img", { name: "Swarm topology" }),
    ).toBeInTheDocument();
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
  it("draws every node the relay can reach, and nothing else", async () => {
    render(<SwarmView />);
    await drawn();
    const names = [...document.querySelectorAll(".swarm-node")].map((n) =>
      (n.textContent || "").trim(),
    );
    expect(names.some((n) => n.includes("outer"))).toBe(true);
    expect(names.some((n) => n.includes("middle"))).toBe(true);
    expect(names.some((n) => n.includes("hidden"))).toBe(true);
    // The list under the map is gone: the map is the interface.
    expect(document.querySelector(".swarm-group")).toBeNull();
    expect(document.querySelector(".swarm-chips")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Hide topology" }),
    ).not.toBeInTheDocument();
  });

  it("enters a node when it is clicked on the map", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    // The middle relay holds no sessions of its own, so there is nothing to
    // open there but the node itself.
    fireEvent.click(mapNode("middle"));
    expect(onOpenNode).toHaveBeenCalledWith(["middle"]);
  });

  // Spotting on the map that a box is asking a question is half the job. The
  // click after it has to land on the question.
  it("opens the waiting session when the node it clicked is asking", async () => {
    const onOpenNode = vi.fn();
    const onOpenSession = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} onOpenSession={onOpenSession} />);
    await drawn();
    fireEvent.click(mapNode("hidden"));
    expect(onOpenNode).not.toHaveBeenCalled();
    const opened = onOpenSession.mock.calls[0]?.[0] as {
      node_path: string[];
      permissionPending?: boolean;
    };
    expect(opened.node_path).toEqual(["middle", "hidden"]);
    expect(opened.permissionPending).toBe(true);
  });

  it("enters a node from the keyboard, and marks it focusable", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    const node = mapNode("middle");
    expect(node.getAttribute("tabindex")).toBe("0");
    fireEvent.keyDown(node, { key: "Enter" });
    fireEvent.keyDown(node, { key: " " });
    expect(onOpenNode).toHaveBeenCalledTimes(2);
    expect(onOpenNode).toHaveBeenLastCalledWith(["middle"]);
  });

  it("does nothing when the relay we are attached to is clicked", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    const root = mapNode("outer");
    expect(root.getAttribute("role")).toBeNull();
    fireEvent.click(root);
    expect(onOpenNode).not.toHaveBeenCalled();
  });

  it("does nothing for a node with no route to it", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        const body = (data: unknown) =>
          ({ ok: true, status: 200, json: async () => data }) as Response;
        if (url.startsWith("/swarm/info")) return body(info);
        if (url.startsWith("/swarm/nodes")) return body({ nodes: [] });
        if (url.startsWith("/swarm/sessions")) {
          return body({ sessions: [], warnings: [] });
        }
        return body({
          ...topology,
          nodes: [
            { uuid: "u-lost", name: "stranded", kind: "agent", online: false },
          ],
          edges: [],
          routes: {},
        });
      }),
    );
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    const lost = mapNode("stranded");
    expect(lost.getAttribute("role")).toBeNull();
    fireEvent.click(lost);
    expect(onOpenNode).not.toHaveBeenCalled();
  });

  it("says which node the app is on, and lights the path to it", async () => {
    render(<SwarmView currentNode={["middle", "hidden"]} />);
    await drawn();
    const here = document.querySelector(".swarm-node.is-current");
    expect(here?.textContent).toContain("hidden");
    expect(here?.textContent).toContain("you are here");
    // Two hops from the relay, so both of them are the live path and nothing
    // else is.
    expect(document.querySelectorAll(".swarm-edge.is-live")).toHaveLength(2);
    expect(
      document.querySelector(".swarm-graph-edges")?.getAttribute("class"),
    ).toContain("is-tracing");
    // The screen reader gets the same fact the picture gives.
    expect(
      document.querySelector(".swarm-graph-summary")?.textContent,
    ).toContain("You are working on hidden");
  });

  it("previews the route to a node while it is hovered", async () => {
    render(<SwarmView onOpenNode={vi.fn()} />);
    await drawn();
    expect(document.querySelectorAll(".swarm-edge.is-preview")).toHaveLength(0);
    fireEvent.mouseEnter(mapNode("hidden"));
    await waitFor(() => {
      expect(document.querySelectorAll(".swarm-edge.is-preview")).toHaveLength(
        2,
      );
    });
    fireEvent.mouseLeave(mapNode("hidden"));
    await waitFor(() => {
      expect(document.querySelectorAll(".swarm-edge.is-preview")).toHaveLength(
        0,
      );
    });
  });

  it("says what each node is doing, and moves only where work is", async () => {
    render(<SwarmView />);
    await drawn();
    await waitFor(() => {
      expect(mapNode("nas02").textContent).toContain("1 running");
    });
    expect(
      mapNode("nas02").querySelector(".swarm-node-halo.is-running"),
    ).not.toBeNull();
    // A node waiting on a person says so, and says it instead of counting: it
    // is the state that needs a human, so it wins over anything running there.
    expect(mapNode("hidden").textContent).toContain("needs an answer");
    expect(
      mapNode("hidden").querySelector(".swarm-node-halo.is-waiting"),
    ).not.toBeNull();
    // A relay owns no sessions, so nothing on it pulses.
    expect(mapNode("middle").querySelector(".swarm-node-halo.is-running")).toBe(
      null,
    );
    // Only the branch carrying a turn travels.
    expect(document.querySelectorAll(".swarm-edge.is-busy")).toHaveLength(1);
  });

  it("leaves the map still when nothing is running", async () => {
    vi.stubGlobal("fetch", stubFetch({ sessions: [] }));
    render(<SwarmView />);
    await drawn();
    await waitFor(() => {
      expect(
        document.querySelectorAll(".swarm-node-halo.is-running"),
      ).toHaveLength(0);
    });
    expect(
      document.querySelectorAll(".swarm-node-halo.is-waiting"),
    ).toHaveLength(0);
    expect(document.querySelectorAll(".swarm-edge.is-busy")).toHaveLength(0);
  });

  // The halo animates off a class, never off a remount, so a poll that reports
  // the same work must leave the element - and its phase - alone.
  it("does not restart the pulse when nothing about the work changed", async () => {
    render(<SwarmView />);
    await drawn();
    await waitFor(() => {
      expect(mapNode("nas02").textContent).toContain("1 running");
    });
    const before = mapNode("nas02").querySelector(".swarm-node-halo");
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-results")).toBeInTheDocument();
    });
    const after = mapNode("nas02").querySelector(".swarm-node-halo");
    expect(after).toBe(before);
    expect(after?.getAttribute("class")).toBe("swarm-node-halo is-running");
  });

  // A media query adds no specificity, so the rules that switch the motion off
  // have to name the same state classes that switched it on.
  it("switches every animation off under reduced motion", () => {
    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    const block = css.slice(
      css.lastIndexOf("@media (prefers-reduced-motion: reduce)"),
    );
    expect(block).toContain(".swarm-node-halo.is-running");
    expect(block).toContain(".swarm-node-halo.is-waiting");
    expect(block).toContain(".swarm-edge.is-busy");
    expect(block).toContain("animation: none;");
  });

  it("shows no rows until something is searched for", async () => {
    render(<SwarmView />);
    await drawn();
    expect(screen.queryByTestId("swarm-results")).not.toBeInTheDocument();
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
      expect(screen.getByTestId("swarm-results")).toBeInTheDocument();
    });
    const rows = screen.getByTestId("swarm-results");
    expect(rows).toHaveTextContent("refactor the parser");
    // Each row names the node it lives on and the route that reaches it.
    expect(rows).toHaveTextContent("nas02");
    expect(rows).not.toHaveTextContent("train the model");
  });

  it("names the route of a session several hops away", async () => {
    render(<SwarmView />);
    await drawn();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "train" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-results")).toHaveTextContent(
        "middle › hidden",
      );
    });
  });

  it("clears the rows when the query is emptied", async () => {
    render(<SwarmView />);
    await drawn();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-results")).toBeInTheDocument();
    });
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "" },
    });
    await waitFor(() => {
      expect(screen.queryByTestId("swarm-results")).not.toBeInTheDocument();
    });
  });

  it("says so when a query matches nothing", async () => {
    render(<SwarmView />);
    await drawn();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "nothing matches this" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-results-empty")).toHaveTextContent(
        "No sessions match",
      );
    });
  });

  it("hands a picked session to its owner", async () => {
    const onOpen = vi.fn();
    render(<SwarmView onOpenSession={onOpen} />);
    await drawn();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => {
      expect(screen.getByText("refactor the parser")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("refactor the parser"));
    expect(onOpen).toHaveBeenCalledTimes(1);
    const opened = onOpen.mock.calls[0]?.[0] as { node_path: string[] };
    expect(opened.node_path).toEqual(["nas02"]);
  });

  it("surfaces a node that could not be reached instead of hiding the rest", async () => {
    vi.stubGlobal("fetch", stubFetch({ warnings: ["gone: not reachable"] }));
    render(<SwarmView />);
    await waitFor(() => {
      expect(screen.getByTestId("swarm-warnings")).toHaveTextContent(
        "gone: not reachable",
      );
    });
    await drawn();
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
  });
});
