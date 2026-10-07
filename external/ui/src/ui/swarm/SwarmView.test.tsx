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
    { uuid: "u-other", name: "other", kind: "relay", online: true },
    { uuid: "u-nas02", name: "nas02", kind: "agent", online: true },
    { uuid: "u-hidden", name: "hidden", kind: "agent", online: true },
  ],
  edges: [
    { from_uuid: "root", to_uuid: "u-nas02", name: "nas02" },
    { from_uuid: "root", to_uuid: "u-mid", name: "middle" },
    { from_uuid: "root", to_uuid: "u-other", name: "other" },
    { from_uuid: "u-mid", to_uuid: "u-hidden", name: "hidden" },
  ],
  routes: {
    "u-nas02": { path: ["nas02"] },
    "u-mid": { path: ["middle"] },
    "u-hidden": { path: ["middle", "hidden"] },
  },
  warnings: [],
};

function widerTopology(): typeof topology {
  const additions = Array.from({ length: 12 }, (_, index) => ({
    uuid: `u-wide-${index}`,
    name: `wide-${index}`,
    kind: "agent",
    online: true,
  }));
  return {
    ...topology,
    nodes: [...topology.nodes, ...additions],
    edges: [
      ...topology.edges,
      ...additions.map((node) => ({
        from_uuid: "root",
        to_uuid: node.uuid,
        name: node.name,
      })),
    ],
    routes: {
      ...topology.routes,
      ...Object.fromEntries(
        additions.map((node) => [node.uuid, { path: [node.name] }]),
      ),
    },
  };
}

let calls: string[] = [];

function stubFetch(
  overrides: {
    warnings?: string[];
    sessions?: typeof sessions;
    topology?: () => typeof topology;
  } = {},
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
      return body(overrides.topology?.() ?? topology);
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

/** The words a node draws on the map, its tooltip left out. */
function drawnWords(node: Element | null): string {
  return [...(node?.querySelectorAll("text") ?? [])]
    .map((t) => (t.textContent || "").trim())
    .filter(Boolean)
    .join(" ");
}

/** The line of words under a node's name, empty when it draws none. */
function meta(node: Element): string {
  return (node.querySelector(".swarm-node-meta")?.textContent || "").trim();
}

function graphViewport(): HTMLElement {
  return screen.getByTestId("swarm-graph-viewport");
}

function graphCamera(): SVGGElement {
  const camera = document.querySelector(".swarm-graph-camera");
  expect(camera, "no graph camera drawn").toBeTruthy();
  return camera as SVGGElement;
}

function sizeGraphViewport(): void {
  Object.defineProperty(graphViewport(), "getBoundingClientRect", {
    configurable: true,
    value: () => new DOMRect(0, 0, 640, 420),
  });
}

async function wheelReady(): Promise<void> {
  await waitFor(() => {
    expect(graphViewport()).toHaveAttribute("data-wheel-ready", "true");
  });
}

function cameraValues(): number[] {
  const values = (graphCamera().getAttribute("transform") || "").match(
    /-?(?:\d+\.?\d*|\.\d+)/g,
  );
  expect(values).not.toBeNull();
  return values!.map(Number);
}

function firePointer(
  target: Element,
  type: "pointerdown" | "pointermove" | "pointerup" | "pointercancel",
  init: { pointerId: number; clientX: number; clientY: number },
): void {
  const event = new Event(type, { bubbles: true, cancelable: true });
  for (const [name, value] of Object.entries({
    ...init,
    pointerType: "mouse",
    button: 0,
  })) {
    Object.defineProperty(event, name, { value });
  }
  fireEvent(target, event);
}

async function drawn(): Promise<void> {
  await waitFor(() => {
    const graph =
      screen.queryByRole("group", { name: "Swarm topology" }) ??
      screen.queryByRole("img", { name: "Swarm topology" });
    expect(graph).toBeInTheDocument();
  });
}

beforeEach(() => {
  calls = [];
  localStorage.clear();
  sessionStorage.clear();
  vi.stubGlobal("fetch", stubFetch());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("SwarmView", () => {
  it("starts as a tree and remembers the graph layout in this browser", async () => {
    const first = render(<SwarmView />);
    await drawn();
    const tree = screen.getByRole("button", { name: "Tree layout" });
    const graph = screen.getByRole("button", { name: "Graph layout" });
    expect(tree).toHaveAttribute("aria-pressed", "true");
    expect(graph).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(graph);
    expect(graph).toHaveAttribute("aria-pressed", "true");
    expect(localStorage.getItem("coddy_swarm_layout")).toBe("graph");
    first.unmount();

    render(<SwarmView />);
    await drawn();
    expect(
      screen.getByRole("button", { name: "Graph layout" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("reads a stored star preference as the graph layout", async () => {
    localStorage.setItem("coddy_swarm_layout", "star");
    render(<SwarmView />);
    await drawn();
    expect(
      screen.getByRole("button", { name: "Graph layout" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("draws smooth cubic wires in the graph layout and orthogonal ones in the tree", async () => {
    const first = render(<SwarmView />);
    await drawn();
    const treeEdges = [
      ...document.querySelectorAll(".swarm-graph-edges .swarm-edge"),
    ].map((e) => e.getAttribute("d"));
    expect(treeEdges.length).toBeGreaterThan(0);
    for (const d of treeEdges) {
      expect(d, `tree wire ${d} should stay orthogonal`).not.toContain("C");
    }

    fireEvent.click(screen.getByRole("button", { name: "Graph layout" }));
    const graphEdges = [
      ...document.querySelectorAll(".swarm-graph-edges .swarm-edge"),
    ].map((e) => e.getAttribute("d"));
    expect(graphEdges.length).toBe(treeEdges.length);
    for (const d of graphEdges) {
      expect(d, `graph wire ${d} should be a cubic`).toContain("C");
    }
    first.unmount();
  });

  it("fits again when the layout mode changes", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    fireEvent.wheel(graphViewport(), {
      deltaY: -240,
      clientX: 320,
      clientY: 210,
    });
    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });

    fireEvent.click(screen.getByRole("button", { name: "Graph layout" }));
    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "false");
  });

  it("pans a fitted tree sideways and returns it centred with Fit", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    // The fit is applied on demand: press it once to land on the fitted frame.
    fireEvent.click(screen.getByRole("button", { name: "Fit graph" }));
    const fitted = graphCamera().getAttribute("transform");
    expect(fitted).not.toBe("translate(0 0) scale(1)");
    const fitValues = cameraValues();
    const viewport = graphViewport();
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 200,
      clientY: 200,
    });
    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 260,
      clientY: 200,
    });
    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });
    // Panning moves the camera without zooming: the scale does not change.
    const panned = cameraValues();
    expect(graphCamera().getAttribute("transform")).not.toBe(fitted);
    expect(panned.at(-1)).toBeCloseTo(fitValues.at(-1) ?? NaN);

    firePointer(viewport, "pointerup", {
      pointerId: 1,
      clientX: 260,
      clientY: 200,
    });
    fireEvent.click(screen.getByRole("button", { name: "Fit graph" }));
    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "false");
    expect(graphCamera().getAttribute("transform")).toBe(fitted);
  });

  it("zooms around the pointer without scrolling the page", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    const event = new WheelEvent("wheel", {
      bubbles: true,
      cancelable: true,
      deltaY: -240,
      clientX: 150,
      clientY: 110,
    });
    graphViewport().dispatchEvent(event);

    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });
    expect(event.defaultPrevented).toBe(true);
  });

  it("pans after four pixels without entering the node", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 105,
      clientY: 100,
    });
    firePointer(viewport, "pointerup", {
      pointerId: 1,
      clientX: 105,
      clientY: 100,
    });
    fireEvent.click(mapNode("nas02"));

    expect(onOpenNode).not.toHaveBeenCalled();
  });

  it("keeps a click on a node when the pointer did not become a drag", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 103,
      clientY: 100,
    });
    firePointer(viewport, "pointerup", {
      pointerId: 1,
      clientX: 103,
      clientY: 100,
    });
    fireEvent.click(mapNode("nas02"));

    expect(onOpenNode).toHaveBeenCalledWith(["nas02"]);
  });

  it("defers pointer capture until the press becomes a drag", async () => {
    // Capturing the pointer on pointerdown retargets the click to the
    // viewport in real browsers, so the node under it never enters. Capture
    // may only begin once the press has crossed the drag slop.
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    const capture = vi.fn();
    Object.defineProperty(viewport, "setPointerCapture", {
      configurable: true,
      value: capture,
    });
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 103,
      clientY: 100,
    });
    expect(capture).not.toHaveBeenCalled();

    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 110,
      clientY: 100,
    });
    expect(capture).toHaveBeenCalledWith(1);
  });

  it("captures both pointers when a pinch begins", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    const capture = vi.fn();
    Object.defineProperty(viewport, "setPointerCapture", {
      configurable: true,
      value: capture,
    });
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    expect(capture).not.toHaveBeenCalled();
    firePointer(viewport, "pointerdown", {
      pointerId: 2,
      clientX: 200,
      clientY: 100,
    });
    expect(capture).toHaveBeenCalledWith(1);
    expect(capture).toHaveBeenCalledWith(2);
  });

  it("drops the camera transition throughout a pinch gesture", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    const viewport = graphViewport();
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    firePointer(viewport, "pointerdown", {
      pointerId: 2,
      clientX: 200,
      clientY: 100,
    });

    expect(viewport).toHaveClass("is-panning");
    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    expect(css).toContain(
      ".swarm-graph-viewport.is-panning .swarm-graph-camera",
    );

    firePointer(viewport, "pointerup", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    expect(viewport).toHaveClass("is-panning");
    firePointer(viewport, "pointercancel", {
      pointerId: 2,
      clientX: 200,
      clientY: 100,
    });
    expect(viewport).not.toHaveClass("is-panning");
  });

  it("keeps the manual camera across an unchanged topology poll", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    fireEvent.wheel(graphViewport(), {
      deltaY: -240,
      clientX: 320,
      clientY: 210,
    });
    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });
    const before = graphCamera().getAttribute("transform");

    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-results")).toBeInTheDocument();
    });
    expect(graphCamera().getAttribute("transform")).toBe(before);
  });

  it("reconciles changed bounds without refitting a manual camera", async () => {
    let activeTopology = topology;
    vi.stubGlobal("fetch", stubFetch({ topology: () => activeTopology }));
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    await wheelReady();
    fireEvent.wheel(graphViewport(), {
      deltaY: -500,
      clientX: 320,
      clientY: 210,
    });
    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });

    activeTopology = widerTopology();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => expect(mapNode("wide-11")).toBeInTheDocument());

    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    expect(cameraValues().every(Number.isFinite)).toBe(true);
  });

  it("fits again when changed bounds meet an untouched camera", async () => {
    let activeTopology = topology;
    vi.stubGlobal("fetch", stubFetch({ topology: () => activeTopology }));
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    const before = graphCamera().getAttribute("transform");

    activeTopology = widerTopology();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => expect(mapNode("wide-11")).toBeInTheDocument());

    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "false");
    expect(graphCamera().getAttribute("transform")).not.toBe(before);
  });

  it("cancels an active pinch when bounds change", async () => {
    let activeTopology = topology;
    vi.stubGlobal("fetch", stubFetch({ topology: () => activeTopology }));
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    firePointer(viewport, "pointerdown", {
      pointerId: 1,
      clientX: 100,
      clientY: 100,
    });
    firePointer(viewport, "pointerdown", {
      pointerId: 2,
      clientX: 200,
      clientY: 100,
    });

    activeTopology = widerTopology();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "parser" },
    });
    await waitFor(() => expect(mapNode("wide-11")).toBeInTheDocument());
    const reconciled = graphCamera().getAttribute("transform");
    firePointer(viewport, "pointermove", {
      pointerId: 1,
      clientX: 40,
      clientY: 100,
    });

    expect(graphCamera().getAttribute("transform")).toBe(reconciled);
  });

  it("offers keyboard zoom and fit controls with accessible names", async () => {
    render(<SwarmView />);
    await drawn();
    sizeGraphViewport();
    const viewport = graphViewport();
    expect(
      screen.getByRole("button", { name: "Zoom out" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Fit graph" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Zoom in" })).toBeInTheDocument();

    fireEvent.keyDown(viewport, { key: "+", shiftKey: true });
    await waitFor(() => {
      expect(graphCamera()).toHaveAttribute("data-user-adjusted", "true");
    });
    fireEvent.keyDown(viewport, { key: "0" });
    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "false");
    fireEvent.keyDown(viewport, { key: "+", ctrlKey: true });
    expect(graphCamera()).toHaveAttribute("data-user-adjusted", "false");
  });

  it("outlines every relay on the active route", async () => {
    render(<SwarmView currentNode={["middle", "hidden"]} />);
    await drawn();
    expect(mapNode("outer")).toHaveClass("is-route-relay");
    expect(mapNode("middle")).toHaveClass("is-route-relay");
    expect(mapNode("other")).not.toHaveClass("is-route-relay");

    fireEvent.click(screen.getByRole("button", { name: "Graph layout" }));
    expect(mapNode("middle")).toHaveClass("is-route-relay");
    expect(mapNode("other")).not.toHaveClass("is-route-relay");

    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    expect(css).toContain(
      ".swarm-node-relay.is-route-relay:not(.is-current) .swarm-node-ring",
    );
  });

  it("exposes the graph and enterable SVG nodes as interactive controls", async () => {
    render(<SwarmView onOpenNode={vi.fn()} />);
    await drawn();
    expect(screen.getByRole("group", { name: "Swarm topology" })).toBe(
      document.querySelector("svg.swarm-graph"),
    );
    expect(screen.queryByRole("img", { name: "Swarm topology" })).toBeNull();
    expect(
      screen.getByRole("button", {
        name: "Open nas02 through this relay",
      }),
    ).toBe(mapNode("nas02"));
  });

  it("drops camera transition while panning and sizes controls for touch", () => {
    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    expect(css).toContain(
      ".swarm-graph-viewport.is-panning .swarm-graph-camera",
    );
    expect(css).toMatch(
      /@media \(max-width: 1199px\) \{[\s\S]*?\.swarm-canvas-control \{[\s\S]*?width: 40px;[\s\S]*?height: 40px;/,
    );
  });

  it("uses the full Russian fit-graph label", () => {
    const russian = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../i18n/messages/ru.ts"),
      "utf8",
    );
    expect(russian).toContain('"swarm.viewport.fit": "Показать граф целиком"');
  });

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
    // An idle agent: nothing is running or waiting there, so the click opens
    // the node itself.
    vi.stubGlobal("fetch", stubFetch({ sessions: [] }));
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    fireEvent.click(mapNode("nas02"));
    expect(onOpenNode).toHaveBeenCalledWith(["nas02"]);
  });

  // A relay on the map is a relay: entering it as a node pointed the chat at a
  // relay's mount, which has no sessions. It opens as the relay it is - its own
  // map, reached through this one (issue #401).
  it("opens a relay it reaches as a relay when it is clicked", async () => {
    const onOpenNode = vi.fn();
    const onOpenRelay = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} onOpenRelay={onOpenRelay} />);
    await drawn();
    fireEvent.click(mapNode("middle"));
    expect(onOpenRelay).toHaveBeenCalledWith(["middle"], "middle");
    expect(onOpenNode).not.toHaveBeenCalled();
  });

  // A click on a node switches the app to it and nothing more: what to do
  // there - the question it asks, its history - is the next click's, on the
  // node's own screens.
  it("switches to a node that is asking rather than opening its question", async () => {
    const onOpenNode = vi.fn();
    const onOpenSession = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} onOpenSession={onOpenSession} />);
    await drawn();
    fireEvent.click(mapNode("hidden"));
    expect(onOpenNode).toHaveBeenCalledWith(["middle", "hidden"]);
    expect(onOpenSession).not.toHaveBeenCalled();
  });

  // The node the app is on is where a click would go: it is not a button, as
  // the relay the app is on is not.
  it("leaves the node the app is on alone", async () => {
    const onOpenNode = vi.fn();
    const onOpenSession = vi.fn();
    render(
      <SwarmView
        currentNode={["nas02"]}
        onOpenNode={onOpenNode}
        onOpenSession={onOpenSession}
      />,
    );
    await drawn();
    const here = mapNode("nas02");
    expect(here.getAttribute("role")).toBeNull();
    fireEvent.click(here);
    fireEvent.keyDown(here, { key: "Enter" });
    expect(onOpenNode).not.toHaveBeenCalled();
    expect(onOpenSession).not.toHaveBeenCalled();
  });

  // A switch starts the app over on the new node, the map with it. The map is
  // the relay's, the same before and after, so it is drawn at once from what
  // it last showed rather than from "Looking…".
  it("draws the map it last showed for this relay at once", async () => {
    const first = render(<SwarmView />);
    await drawn();
    first.unmount();
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    render(<SwarmView />);
    expect(
      screen.getByRole("group", { name: "Swarm topology" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Looking…")).toBeNull();
  });

  it("enters a node from the keyboard, and marks it focusable", async () => {
    const onOpenRelay = vi.fn();
    render(<SwarmView onOpenNode={() => {}} onOpenRelay={onOpenRelay} />);
    await drawn();
    const node = mapNode("middle");
    expect(node.getAttribute("tabindex")).toBe("0");
    fireEvent.keyDown(node, { key: "Enter" });
    fireEvent.keyDown(node, { key: " " });
    expect(onOpenRelay).toHaveBeenCalledTimes(2);
    expect(onOpenRelay).toHaveBeenLastCalledWith(["middle"], "middle");
  });

  // Over a node, the map is read from the relay directly, and a click on the
  // relay connects to it: the app leaves the node for the relay (issue #401).
  it("connects to the relay from the map opened over a node", async () => {
    // Asked by its full address: the relay, not the page's environment.
    const relayAnswers = stubFetch();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        relayAnswers(String(input).replace("http://relay.test", "")),
      ),
    );
    const onOpenRelay = vi.fn();
    render(
      <SwarmView
        onOpenNode={() => {}}
        onOpenRelay={onOpenRelay}
        relay={{ baseUrl: "http://relay.test", token: "t" }}
      />,
    );
    await drawn();
    const root = mapNode("outer");
    expect(root.getAttribute("role")).toBe("button");
    fireEvent.click(root);
    expect(onOpenRelay).toHaveBeenCalledWith([], "outer");
  });

  // On the relay itself there is nothing to connect to: the relay is not a
  // button, and a click on it reloads nothing.
  // The machine the page runs on is drawn above the relay, wired to it, and a
  // click on it goes back to Local. Its wire is how the page reaches the swarm,
  // not a link of the swarm, so the relay's card does not count it.
  it("draws the machine the page runs on above the relay, and opens it", async () => {
    const onOpenLocal = vi.fn();
    render(
      <SwarmView
        client={{ name: "laptop" }}
        onOpenLocal={onOpenLocal}
        currentNode={["nas02"]}
      />,
    );
    await drawn();
    const client = document.querySelector(".swarm-node-client");
    expect(drawnWords(client)).toBe("laptop");
    expect(client?.querySelector("title")?.textContent).toContain("Local");
    // The disc's shape says it is a relay; a healthy one carries no meta
    // line - only trouble (no route, offline) gets one.
    expect(meta(mapNode("outer"))).toBe("");
    expect(mapNode("outer").querySelector("title")?.textContent).toContain(
      "relay",
    );
    fireEvent.click(client as Element);
    expect(onOpenLocal).toHaveBeenCalledTimes(1);
  });

  // A node that dials out carries a badge and a dotted wire; the words under
  // it are for what the drawing cannot say, so its work moves up to that line.
  it("says nothing under a working node that the drawing already says", async () => {
    const tunnelled = {
      ...topology,
      nodes: topology.nodes.map((n) =>
        n.uuid === "u-nas02" ? { ...n, transport: "tunnel" } : n,
      ),
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.startsWith("/swarm/topology")) {
          return {
            ok: true,
            status: 200,
            json: async () => tunnelled,
          } as Response;
        }
        return stubFetch()(input);
      }),
    );
    render(<SwarmView />);
    await drawn();
    const nas = mapNode("nas02");
    expect(nas.querySelector(".swarm-node-badge")).not.toBeNull();
    expect(meta(nas)).toBe("");
    expect(nas.querySelector("title")?.textContent).toContain("dials out");
    const work = nas.querySelector(".swarm-node-work text");
    expect(work?.textContent).toContain("running");
    expect(work?.getAttribute("y")).toBe("62");
    // A plain agent says nothing about being one either.
    expect(meta(mapNode("hidden"))).toBe("");
  });

  // The tiers say which way a hop runs; arrowheads on every wire said it again.
  it("draws its wires without arrowheads", async () => {
    render(<SwarmView />);
    await drawn();
    expect(document.querySelectorAll(".swarm-edge").length).toBeGreaterThan(0);
    expect(document.querySelector("marker")).toBeNull();
    for (const wire of document.querySelectorAll(".swarm-edge")) {
      expect(wire.getAttribute("marker-end")).toBeNull();
      expect(wire.getAttribute("marker-start")).toBeNull();
    }
  });

  // A wire is named by the mount the relay reaches the node under. When that is
  // the node's own name, the chip under the node already says it.
  it("names a wire only when the mount differs from the node's name", async () => {
    const renamed = {
      ...topology,
      edges: topology.edges.map((e) =>
        e.to_uuid === "u-hidden" ? { ...e, name: "backup" } : e,
      ),
    };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.startsWith("/swarm/topology")) {
          return {
            ok: true,
            status: 200,
            json: async () => renamed,
          } as Response;
        }
        return stubFetch()(input);
      }),
    );
    render(<SwarmView />);
    await drawn();
    const chips = [...document.querySelectorAll(".swarm-edge-label")].map(
      (t) => t.textContent,
    );
    expect(chips).toEqual(["backup"]);
  });

  it("leaves the relay the app is on alone", async () => {
    const onOpenRelay = vi.fn();
    render(<SwarmView onOpenNode={() => {}} onOpenRelay={onOpenRelay} />);
    await drawn();
    const root = mapNode("outer");
    expect(root.getAttribute("role")).toBeNull();
    fireEvent.click(root);
    expect(onOpenRelay).not.toHaveBeenCalled();
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
    // The ring is the mark; a line of text under it said the same thing again.
    expect(drawnWords(here)).not.toContain("you are here");
    expect(here?.querySelector("title")?.textContent).toContain("you are here");
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

  // On the relay itself the relay is where the app is: it carries the ring a
  // node carries when the app is on it, and the wire from this machine, when
  // the map draws it, is the route in use.
  it("rings the relay the app is on, and draws the way to it as the route", async () => {
    render(<SwarmView rootCurrent client={{ name: "laptop" }} />);
    await drawn();
    const current = document.querySelectorAll(".swarm-node.is-current");
    expect(current).toHaveLength(1);
    expect(current[0]).toBe(mapNode("outer"));
    expect(current[0]?.querySelector("title")?.textContent).toContain(
      "you are here",
    );
    const live = document.querySelectorAll(".swarm-edge.is-live");
    expect(live).toHaveLength(1);
    expect(
      document.querySelector(".swarm-node-client")?.getAttribute("class"),
    ).toContain("is-on-route");
    expect(mapNode("outer")).toHaveClass("is-route-relay", "is-current");
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

  // Below 1200px the shell's backdrop rises to 60 and #/swarm opens it, so a
  // dock under it hands every tap to the backdrop, which closes the screen.
  it("takes taps on a phone: the dock sits above the backdrop and below the top bar", () => {
    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    const mobileBackdrop =
      /@media \(max-width: 1199px\) \{[^@]*?\.backdrop \{\s*z-index: (\d+)/.exec(
        css,
      );
    const mobileDock =
      /@media \(max-width: 1199px\) \{\s*(?:\/\*[^*]*\*\/\s*)?\.swarm-dock-cluster \{([^}]*)\}/.exec(
        css,
      );
    expect(mobileBackdrop).not.toBeNull();
    expect(mobileDock).not.toBeNull();
    const rule = mobileDock![1]!;
    const z = /z-index:\s*(\d+)/.exec(rule);
    expect(z).not.toBeNull();
    expect(Number(z![1])).toBeGreaterThan(Number(mobileBackdrop![1]));
    expect(rule).toMatch(/top:\s*calc\(var\(--coddy-mobile-top-inset\)/);
  });

  // The map opens in a dock like the documentation reader's, and the two are one
  // width: the map's dock spanned the whole screen right of the rail while the
  // reader stopped at 1350px, and the map inside kept to 1100px of it, which
  // left bare strips on both sides of a wide screen (issue #401).
  it("opens in a dock as wide as the documentation reader's, and the map fills it", () => {
    const css = readFileSync(
      join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
      "utf8",
    );
    const top = (selector: string) =>
      new RegExp(`^\\${selector}\\s*\\{([^}]*)\\}`, "m").exec(css)?.[1] ?? "";
    for (const dock of [".swarm-dock-cluster", ".docs-dock-cluster"]) {
      const rule = top(dock);
      expect(rule, dock).toMatch(/width:\s*var\(--coddy-dock-width\)/);
      expect(rule, dock).toMatch(/margin-inline:\s*auto/);
    }
    const view = top(".swarm-view");
    expect(view).not.toMatch(/max-width/);
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
        "Nothing matches",
      );
    });
  });

  it("finds a node by name among the sessions", async () => {
    const onOpenNode = vi.fn();
    render(<SwarmView onOpenNode={onOpenNode} />);
    await drawn();
    fireEvent.change(screen.getByTestId("swarm-search"), {
      target: { value: "nas02" },
    });
    await waitFor(() => {
      expect(screen.getByTestId("swarm-node-hit-nas02")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId("swarm-node-hit-nas02"));
    expect(onOpenNode).toHaveBeenCalledWith(["nas02"]);
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
});
