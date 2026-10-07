import { useCallback, useId, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  CLIENT_UUID,
  NODE_METRICS as M,
  connectorFor,
  graphConnectorFor,
  layoutTopology,
  rootRouteEdgeIds,
  routeEdgeIds,
  type Connector,
  type PlacedEdge,
  type PlacedNode,
  type TierRow,
  type TopologyLayout,
} from "./layout";
import { layoutTopologyGraph } from "./forceLayout";
import type { SwarmLayoutMode } from "./layoutMode";
import { useGraphViewport } from "./useGraphViewport";
import type { NodeActivity } from "./routes";
import type { SwarmTopology } from "./types";
import { useT } from "../i18n/I18nProvider";

/** Shared empties, so a graph without work does not rebuild its memos. */
const NO_ACTIVITY: Record<string, NodeActivity> = {};
const NO_EDGES: ReadonlySet<string> = new Set<string>();

/** What a node is doing, in the order a person has to deal with it. */
type WorkState = "idle" | "running" | "waiting";

/**
 * Draws the swarm as a network, and is the way into it: the relay you are
 * attached to on the top row, then whatever it reaches a hop lower, then
 * whatever that reaches. Clicking a node connects to it. A spine down the left
 * counts the hops, relays are cards carrying a router mark, and agents are
 * circles with their name on a chip below.
 *
 * The map also carries the live picture - where the app is now, the path it
 * took, and which nodes are working - so nothing about the swarm has to be
 * repeated in a list underneath.
 *
 * Hand-rolled SVG rather than a graph library: the arrangement and the wire
 * geometry are pure functions tested on their own, and the drawing is a few
 * dozen elements.
 */
export function TopologyGraph(props: {
  topology: SwarmTopology;
  /** Joined route of the node the app is driving right now, if any. */
  currentNode?: string | null;
  /** The app is on the relay the map is drawn for, not on a node under it. */
  rootCurrent?: boolean;
  /** Work per node, keyed by joined route. */
  activity?: Record<string, NodeActivity>;
  onEnterNode?: (node: PlacedNode) => void;
  /**
   * The relay the map is drawn for can be entered: the map is open over a node
   * and a click on the relay connects to it. On the relay itself there is
   * nothing to connect to.
   */
  rootEnterable?: boolean;
  /**
   * The machine the page runs on, drawn above the relay as where the
   * connection starts (issue #401), named by its host name. A click on it
   * opens that machine.
   */
  client?: { name: string };
  onEnterClient?: () => void;
  layoutMode: SwarmLayoutMode;
  onLayoutModeChange: (mode: SwarmLayoutMode) => void;
  resetKey: string;
  /**
   * Storage key for the camera (zoom and pan) the operator framed this map
   * with. Entering a node remounts the whole app, and the stored camera is
   * what keeps the map from fitting all the way back out.
   */
  cameraKey?: string;
}) {
  const { t, tp } = useT();
  // SwarmView re-polls every five seconds; without this every poll rebuilds the
  // arrangement even when nothing about the swarm moved.
  const clientName = props.client?.name ?? "";
  const layout = useMemo(
    () =>
      (props.layoutMode === "graph" ? layoutTopologyGraph : layoutTopology)(
        props.topology,
        clientName ? { client: { name: clientName } } : {},
      ),
    [props.topology, clientName, props.layoutMode],
  );
  // Two graphs on one page would otherwise share the id their description is
  // found by. useId can contain colons, so only id-safe characters survive.
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const descId = `swarm-desc-${uid}`;
  const enter = props.onEnterNode;
  const activity = props.activity ?? NO_ACTIVITY;
  const current = props.currentNode || "";
  const rootCurrent = !current && props.rootCurrent === true;
  const { width, height, spineX } = layout;
  const bounds = useMemo(
    () => ({ x: 0, y: 0, width, height }),
    [width, height],
  );
  const viewport = useGraphViewport({
    bounds,
    resetKey: props.resetKey,
    ...(props.cameraKey ? { persistKey: props.cameraKey } : {}),
  });

  // Previewing a route on hover needs no data, only which node the pointer is
  // over. Focus feeds the same state so the keyboard sees what the mouse does.
  const [preview, setPreview] = useState<string>("");

  const liveEdges = useMemo(
    () =>
      current
        ? routeEdgeIds(layout, current)
        : rootCurrent
          ? rootRouteEdgeIds(layout)
          : NO_EDGES,
    [layout, current, rootCurrent],
  );
  const previewEdges = useMemo(
    () =>
      preview && preview !== current ? routeEdgeIds(layout, preview) : NO_EDGES,
    [layout, preview, current],
  );
  // Every hop that carries a turn in flight, so the busy branch of the swarm is
  // visible without opening anything.
  const busyEdges = useMemo(
    () => busyRoutes(layout, activity),
    [layout, activity],
  );
  // The nodes the live path passes through, so everything else can recede.
  const onRoute = useMemo(() => {
    const out = new Set<string>();
    if (liveEdges.size === 0) {
      return out;
    }
    for (const e of layout.edges) {
      if (liveEdges.has(e.id)) {
        out.add(e.from.uuid);
        out.add(e.to.uuid);
      }
    }
    return out;
  }, [layout, liveEdges]);

  const tracing = liveEdges.size > 0 || previewEdges.size > 0;

  const tierName = (row: TierRow): string => {
    if (row.depth < 0) {
      return t("swarm.tier.client");
    }
    if (!row.reachable) {
      return t("swarm.state.noRoute");
    }
    return row.depth === 0
      ? t("swarm.tier.attached")
      : tp("swarm.tier.hop", row.depth);
  };

  const isCurrent = (n: PlacedNode): boolean =>
    current
      ? n.path.length > 0 && n.path.join("/") === current
      : rootCurrent && n.kind === "relay" && n.depth === 0;

  // The words under a node are for what the drawing cannot say. The shape says
  // relay or agent, the ring says where the app is, the badge and the dotted
  // wire say a node dials out, the screen glyph and the tier say this machine:
  // spelled out again under each of them they were noise. What is left is
  // trouble - no route, offline - and how much a relay carries.
  const metaOf = (n: PlacedNode): string => {
    if (n.kind === "client") {
      return "";
    }
    if (n.depth > 0 && n.path.length === 0) {
      return t("swarm.state.noRoute");
    }
    if (!n.online) {
      return t("swarm.state.offline");
    }
    return "";
  };

  // What the drawing says, in words, for the tooltip a hover shows.
  const kindOf = (n: PlacedNode): string => {
    if (n.kind === "client") {
      return t("env.local");
    }
    if (isCurrent(n)) {
      return t("swarm.node.here");
    }
    if (n.kind === "relay") {
      return t("swarm.state.relay");
    }
    return n.transport === "tunnel"
      ? t("swarm.state.dialsOut")
      : t("swarm.state.agent");
  };

  const workOf = (n: PlacedNode): NodeActivity | null => {
    // The root has no route of its own, and a stranded node's empty path would
    // borrow the root's key. Neither can own a session anyway.
    if (n.path.length === 0) {
      return null;
    }
    return activity[n.path.join("/")] ?? null;
  };

  const workLine = (work: NodeActivity | null): string => {
    if (!work || work.sessions === 0) {
      return "";
    }
    if (work.waiting > 0) {
      return t("swarm.activity.needsAnswer");
    }
    if (work.running > 0) {
      return tp("swarm.activity.running", work.running);
    }
    return tp("swarm.activity.sessions", work.sessions);
  };

  // The SVG is role="group" rather than role="img": role="img" would collapse
  // the subtree, and the interactive per-node buttons inside must stay in the
  // accessibility tree. This paragraph is a supplemental description of the
  // picture (via aria-describedby), and it has to carry the live half of it
  // too.
  const summary = [
    ...layout.tiers.map((row) =>
      t("swarm.graph.tierNodes", {
        tier: tierName(row),
        names: layout.nodes
          .filter((n) => n.depth === row.depth)
          .map((n) => n.name)
          .join(", "),
      }),
    ),
    hereSentence(layout, current, t),
    namesSentence(layout, activity, "running", (names) =>
      t("swarm.graph.busy", { names }),
    ),
    namesSentence(layout, activity, "waiting", (names) =>
      t("swarm.graph.waitingOn", { names }),
    ),
  ]
    .filter(Boolean)
    .join(" ");

  // Every node with a route is entered. The relay the map is drawn for is
  // entered from a map opened over a node, where a click on it connects to the
  // relay (issue #401); on the relay itself it stays put, as the node the app
  // is on does: a click there would go where the app already is. A node the
  // relay knows but has no route to is left alone.
  const rootEnterable = props.rootEnterable === true;
  const enterClient = props.onEnterClient;
  const enterable = useCallback(
    (n: PlacedNode): boolean =>
      n.kind === "client"
        ? !!enterClient
        : !!enter &&
          !(current && n.path.length > 0 && n.path.join("/") === current) &&
          (n.depth === 0 ? rootEnterable : n.path.length > 0),
    [enter, enterClient, rootEnterable, current],
  );

  // A way round and a dead link go down first, so a route in use always paints
  // over them.
  const edges = [...layout.edges].sort(
    (a, b) =>
      weight(a, liveEdges, previewEdges, busyEdges) -
      weight(b, liveEdges, previewEdges, busyEdges),
  );
  // Deliberately not re-sorted by state. The nodes are keyed by uuid, so
  // reordering them moves the live <g> in the DOM, which blurs the node the
  // reader just activated and reshuffles the tab order under them. Cards never
  // overlap anyway - a row leaves 132px between them and a tier 120px - so
  // nothing can cover a ring.
  const nodes = layout.nodes;
  const first = layout.tiers[0];
  const last = layout.tiers[layout.tiers.length - 1];
  const graph = props.layoutMode === "graph";

  return (
    <div className="swarm-graph-panel">
      <p id={descId} className="swarm-graph-summary">
        {summary}
      </p>
      <div
        ref={viewport.viewportRef}
        className={`swarm-graph-viewport${viewport.isPanning ? " is-panning" : ""}`}
        data-testid="swarm-graph-viewport"
        data-wheel-ready={String(viewport.wheelReady)}
        tabIndex={0}
        {...viewport.stageProps}
      >
        <svg
          className={`swarm-graph${graph ? " swarm-graph--graph" : ""}`}
          width="100%"
          height="100%"
          role="group"
          aria-label={t("swarm.graph.aria")}
          aria-describedby={descId}
        >
          <g
            className="swarm-graph-camera"
            transform={viewport.transform}
            data-user-adjusted={String(viewport.userAdjusted)}
            data-instant={String(viewport.instant)}
          >
            {!graph ? (
              <g className="swarm-graph-spine" aria-hidden="true">
                {first && last ? (
                  <line
                    className="swarm-spine-rule"
                    x1={spineX}
                    y1={first.y - 34}
                    x2={spineX}
                    y2={last.y + 34}
                  />
                ) : null}
                {layout.tiers.map((row) => (
                  <g
                    key={row.depth}
                    className={
                      row.reachable ? "swarm-tier" : "swarm-tier is-parked"
                    }
                  >
                    <line
                      className="swarm-tier-tick"
                      x1={spineX - 5}
                      y1={row.y}
                      x2={spineX + 7}
                      y2={row.y}
                    />
                    <text
                      className="swarm-tier-label"
                      x={spineX - 12}
                      y={row.y - 1}
                      textAnchor="end"
                    >
                      {tierName(row)}
                    </text>
                    <text
                      className="swarm-tier-count"
                      x={spineX - 12}
                      y={row.y + 13}
                      textAnchor="end"
                    >
                      {tp("swarm.summary.nodes", row.count)}
                    </text>
                  </g>
                ))}
              </g>
            ) : null}

            {/* A wire crossing a node must never swallow a click meant for it. */}
            <g
              className={`swarm-graph-edges${tracing ? " is-tracing" : ""}`}
              aria-hidden="true"
            >
              {edges.map((e) => (
                <Wire
                  key={e.id}
                  edge={e}
                  connector={graph ? graphConnectorFor : connectorFor}
                  live={liveEdges.has(e.id)}
                  preview={previewEdges.has(e.id)}
                  busy={busyEdges.has(e.id)}
                />
              ))}
            </g>

            <g className={`swarm-graph-nodes${tracing ? " is-tracing" : ""}`}>
              {nodes.map((n) => {
                const work = workOf(n);
                return (
                  <Node
                    key={n.uuid}
                    node={n}
                    meta={metaOf(n)}
                    kind={kindOf(n)}
                    work={workLine(work)}
                    state={stateOf(work)}
                    current={isCurrent(n)}
                    onRoute={onRoute.has(n.uuid)}
                    consumeGestureClick={viewport.consumeGestureClick}
                    {...(enterable(n)
                      ? n.kind === "client"
                        ? {
                            onEnter: () => enterClient?.(),
                            enterLabel: t("swarm.graph.enterClient"),
                          }
                        : {
                            onEnter: enter,
                            enterLabel:
                              n.kind === "relay"
                                ? t("swarm.graph.enterRelay", { node: n.name })
                                : t("swarm.graph.enter", { node: n.name }),
                          }
                      : {})}
                    onPreview={setPreview}
                  />
                );
              })}
            </g>
          </g>
        </svg>
        <div
          className="swarm-layout-control"
          role="group"
          aria-label={t("swarm.layout.label")}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <CanvasButton
            label={t("swarm.layout.tree")}
            pressed={!graph}
            onClick={() => props.onLayoutModeChange("tree")}
          >
            <TreeIcon />
          </CanvasButton>
          <CanvasButton
            label={t("swarm.layout.graph")}
            pressed={graph}
            onClick={() => props.onLayoutModeChange("graph")}
          >
            <StarIcon />
          </CanvasButton>
        </div>
        <div
          className="swarm-viewport-control"
          onPointerDown={(event) => event.stopPropagation()}
        >
          <CanvasButton
            label={t("swarm.viewport.zoomOut")}
            onClick={viewport.zoomOut}
          >
            <MinusIcon />
          </CanvasButton>
          <CanvasButton label={t("swarm.viewport.fit")} onClick={viewport.fit}>
            <FitIcon />
          </CanvasButton>
          <CanvasButton
            label={t("swarm.viewport.zoomIn")}
            onClick={viewport.zoomIn}
          >
            <PlusIcon />
          </CanvasButton>
        </div>
      </div>

      {/* HTML rather than SVG so it wraps on a phone instead of setting a
          minimum width the graph would then have to scroll to. */}
      <ul className="swarm-graph-legend" aria-label={t("swarm.graph.legend")}>
        <LegendItem kind="route" label={t("swarm.state.route")} />
        <LegendItem kind="idle" label={t("swarm.state.wayRound")} />
        <LegendItem kind="dial" label={t("swarm.state.dialsOut")} />
        <LegendItem kind="down" label={t("swarm.state.offline")} />
      </ul>
    </div>
  );
}

function CanvasButton(props: {
  label: string;
  pressed?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className="swarm-canvas-control"
      aria-label={props.label}
      title={props.label}
      {...(props.pressed === undefined
        ? {}
        : { "aria-pressed": props.pressed })}
      onClick={(event) => {
        event.stopPropagation();
        props.onClick();
      }}
    >
      {props.children}
    </button>
  );
}

function TreeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 3v10M3 5h5M3 10h5M8 5v-2M8 10v2" />
      <circle cx="8" cy="3" r="1.4" />
      <circle cx="8" cy="12" r="1.4" />
    </svg>
  );
}

function StarIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 8 3 3M8 8h5M8 8l-4 5M8 8l2-5" />
      <circle cx="8" cy="8" r="1.5" />
      <circle cx="3" cy="3" r="1.2" />
      <circle cx="13" cy="8" r="1.2" />
      <circle cx="4" cy="13" r="1.2" />
      <circle cx="10" cy="3" r="1.2" />
    </svg>
  );
}

function MinusIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 8h10" />
    </svg>
  );
}

function PlusIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 8h10M8 3v10" />
    </svg>
  );
}

function FitIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10" />
    </svg>
  );
}

/**
 * A link, its state, and the name a route would call it by when that is not
 * the name the node under it already carries. No arrowheads: the tiers say
 * which way a hop runs, and the line's own stroke says the rest.
 */
function Wire(props: {
  edge: PlacedEdge;
  connector: (edge: PlacedEdge) => Connector;
  live: boolean;
  preview: boolean;
  busy: boolean;
}) {
  const e = props.edge;
  const c = props.connector(e);
  const dead = !e.to.online;
  const dials = e.to.transport === "tunnel";
  const cls = [
    "swarm-edge",
    e.alternate ? "is-alternate" : "",
    dead ? "is-down" : "",
    dials ? "is-dialled" : "",
    props.live ? "is-live" : "",
    props.preview ? "is-preview" : "",
    props.busy ? "is-busy" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const label = e.name === e.to.name ? "" : clip(e.name, 16);
  const plate = chipWidth(label, 10, 9);
  return (
    <g
      className={[
        "swarm-edge-group",
        c.peer ? "is-peer" : "",
        // The label plate's dashed outline is selected off the group, so the
        // state has to be here and not only on the path inside it.
        e.alternate ? "is-alternate" : "",
        props.live || props.preview ? "is-traced" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      <path className={cls} d={c.d} />
      {label ? (
        <g
          className="swarm-edge-chip"
          transform={`translate(${c.labelX},${c.labelY})`}
        >
          <rect x={-plate / 2} y={-8} width={plate} height={16} rx={8} />
          <text className="swarm-edge-label" y={3} textAnchor="middle">
            {label}
          </text>
        </g>
      ) : null}
    </g>
  );
}

function Node(props: {
  node: PlacedNode;
  /** The line under the name; empty when the drawing already says it all. */
  meta: string;
  /** What the drawing says, in words: for the tooltip only. */
  kind: string;
  /** Empty when nothing is running there and nothing is parked there. */
  work: string;
  state: WorkState;
  current: boolean;
  onRoute: boolean;
  consumeGestureClick: () => boolean;
  onEnter?: (node: PlacedNode) => void;
  enterLabel?: string;
  onPreview: (route: string) => void;
}) {
  const n = props.node;
  const enter = props.onEnter;
  const route = n.path.join("/");
  const cls = [
    "swarm-node",
    n.kind === "relay" ? "swarm-node-relay" : "swarm-node-agent",
    n.kind === "client" ? "swarm-node-client" : "",
    n.depth === 0 ? "is-root" : "",
    n.online ? "is-online" : "is-offline",
    n.depth > 0 && n.path.length === 0 ? "is-stranded" : "",
    props.current ? "is-current" : "",
    props.onRoute ? "is-on-route" : "",
    n.kind === "relay" && props.onRoute ? "is-route-relay" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const title = [n.name, props.kind, props.meta, props.work, props.enterLabel]
    .filter(Boolean)
    .join(" · ");
  return (
    <g
      className={cls}
      transform={`translate(${n.x},${n.y})`}
      onClick={
        enter
          ? () => {
              if (!props.consumeGestureClick()) enter(n);
            }
          : undefined
      }
      role={enter ? "button" : undefined}
      tabIndex={enter ? 0 : undefined}
      aria-label={enter ? props.enterLabel : undefined}
      onMouseEnter={() => props.onPreview(route)}
      onMouseLeave={() => props.onPreview("")}
      onFocus={() => props.onPreview(route)}
      onBlur={() => props.onPreview("")}
      onKeyDown={
        enter
          ? (ev) => {
              if (ev.key === "Enter" || ev.key === " ") {
                ev.preventDefault();
                enter(n);
              }
            }
          : undefined
      }
    >
      <title>{title}</title>
      {n.kind === "relay" ? (
        <RelayDisc node={n} meta={props.meta} state={props.state} />
      ) : (
        <AgentDisc
          node={n}
          meta={props.meta}
          state={props.state}
          glyph={n.kind === "client" ? "screen" : "prompt"}
        />
      )}
      {props.work ? (
        <WorkLine
          text={props.work}
          state={props.state}
          y={
            n.kind === "relay"
              ? M.relayActivityDrop
              : props.meta
                ? M.agentActivityDrop
                : M.agentMetaDrop
          }
        />
      ) : null}
    </g>
  );
}

/** A relay routes: a disc a touch bigger than an agent's, with the router
 *  mark on an accent tile, its name on the chip below like every node. */
function RelayDisc(props: {
  node: PlacedNode;
  meta: string;
  state: WorkState;
}) {
  const n = props.node;
  const r = M.relayRadius;
  const label = clip(n.name, 18);
  const chip = chipWidth(label, 11, 10);
  return (
    <>
      <circle className="swarm-node-hit" r={r + 12} />
      <circle className={haloClass(props.state)} r={r + 9} />
      {/* Elevation without a filter: a darker plate peeking out below, which
          reads as a shadow on light and dark alike. */}
      <circle className="swarm-node-shadow" cy={3} r={r - 1} />
      <circle className="swarm-node-ring" r={r + 6} />
      <circle className="swarm-node-body" r={r} />
      <rect
        className="swarm-node-tile"
        x={-M.tile / 2}
        y={-M.tile / 2}
        width={M.tile}
        height={M.tile}
        rx={M.tileRadius}
      />
      <RouterMark cx={0} />
      <StatusDot x={r * 0.7} y={-r * 0.7} online={n.online} />
      {n.transport === "tunnel" ? <DialBadge x={-r * 0.7} y={r * 0.7} /> : null}
      <g
        className="swarm-node-chip"
        transform={`translate(0,${M.relayChipDrop})`}
      >
        <rect
          x={-chip / 2}
          y={-M.chipHeight / 2}
          width={chip}
          height={M.chipHeight}
          rx={M.chipHeight / 2}
        />
        <text y={4} textAnchor="middle">
          {label}
        </text>
      </g>
      {props.meta ? (
        <text
          className="swarm-node-meta"
          y={M.relayMetaDrop}
          textAnchor="middle"
        >
          {clip(props.meta, 22)}
        </text>
      ) : null}
    </>
  );
}

/**
 * An agent does the work: a circle round a prompt, its name on a chip below.
 * The machine the page runs on is drawn the same way round a screen, since it
 * is an agent too - the one this browser is at.
 */
function AgentDisc(props: {
  node: PlacedNode;
  meta: string;
  state: WorkState;
  glyph?: "prompt" | "screen";
}) {
  const n = props.node;
  const r = M.agentRadius;
  const label = clip(n.name, 16);
  const chip = chipWidth(label, 11, 10);
  return (
    <>
      <circle className="swarm-node-hit" r={r + 12} />
      <circle className={haloClass(props.state)} r={r + 9} />
      <circle className="swarm-node-shadow" cy={3} r={r - 1} />
      <circle className="swarm-node-ring" r={r + 6} />
      <circle className="swarm-node-body" r={r} />
      {props.glyph === "screen" ? (
        <g className="swarm-node-glyph">
          <rect
            x={-0.4 * r}
            y={-0.34 * r}
            width={0.8 * r}
            height={0.52 * r}
            rx={0.08 * r}
          />
          <path d={`M${-0.5 * r} ${0.34 * r} H${0.5 * r}`} />
        </g>
      ) : (
        <g className="swarm-node-glyph">
          <path
            d={`M${-0.327 * r} ${-0.231 * r} L${-0.096 * r} 0 L${-0.327 * r} ${0.231 * r}`}
          />
          <path d={`M${0.058 * r} ${0.25 * r} H${0.346 * r}`} />
        </g>
      )}
      <StatusDot x={r * 0.7} y={-r * 0.7} online={n.online} />
      {n.transport === "tunnel" ? <DialBadge x={-r * 0.7} y={r * 0.7} /> : null}
      <g className="swarm-node-chip" transform={`translate(0,${M.chipDrop})`}>
        <rect
          x={-chip / 2}
          y={-M.chipHeight / 2}
          width={chip}
          height={M.chipHeight}
          rx={M.chipHeight / 2}
        />
        <text y={4} textAnchor="middle">
          {label}
        </text>
      </g>
      {props.meta ? (
        <text
          className="swarm-node-meta"
          y={M.agentMetaDrop}
          textAnchor="middle"
        >
          {clip(props.meta, 22)}
        </text>
      ) : null}
    </>
  );
}

/**
 * What the node is doing, under everything else it says. A running node also
 * carries a dot, so the state survives greyscale and a glance from across the
 * room; a node waiting on a person says so in words, which is the only form
 * that cannot be mistaken for progress.
 */
function WorkLine(props: { text: string; state: WorkState; y: number }) {
  const label = clip(props.text, 22);
  const dotted = props.state === "running";
  const w = textWidth(label, 10);
  return (
    <g className={`swarm-node-work is-${props.state}`}>
      {dotted ? (
        <circle
          className="swarm-node-work-dot"
          cx={-(w + 10) / 2 + 3}
          cy={props.y - 3.5}
          r={3}
        />
      ) : null}
      <text x={dotted ? 5 : 0} y={props.y} textAnchor="middle">
        {label}
      </text>
    </g>
  );
}

/** Traffic both ways through one box: what makes a card read as a router. */
function RouterMark(props: { cx: number }) {
  const reach = M.tile * 0.42;
  const lane = M.tile * 0.11;
  const head = M.tile * 0.115;
  const x = props.cx;
  return (
    <g className="swarm-node-mark">
      <path d={`M${x - reach} ${-lane} H${x + reach}`} />
      <path
        d={`M${x + reach - head} ${-lane - head} L${x + reach} ${-lane} L${x + reach - head} ${-lane + head}`}
      />
      <path d={`M${x + reach} ${lane} H${x - reach}`} />
      <path
        d={`M${x - reach + head} ${lane - head} L${x - reach} ${lane} L${x - reach + head} ${lane + head}`}
      />
    </g>
  );
}

/**
 * Liveness cannot ride on colour alone: the two dots are the same grey in
 * greyscale. Offline is hollow and struck through, and its body dashes too.
 */
function StatusDot(props: { x: number; y: number; online: boolean }) {
  const r = M.statusRadius;
  if (props.online) {
    return (
      <circle className="swarm-node-dot" cx={props.x} cy={props.y} r={r} />
    );
  }
  return (
    <g
      className="swarm-node-dot-off"
      transform={`translate(${props.x},${props.y})`}
    >
      <circle r={r} />
      <path d={`M${-r * 0.56} ${r * 0.56} L${r * 0.56} ${-r * 0.56}`} />
    </g>
  );
}

/** Says the connection was opened from the far end, outward to the relay. */
function DialBadge(props: { x: number; y: number }) {
  const r = M.badgeRadius;
  return (
    <g
      className="swarm-node-badge"
      transform={`translate(${props.x},${props.y})`}
    >
      <circle r={r} />
      <path d={`M0 ${r * 0.52} V${-r * 0.42}`} />
      <path
        d={`M${-r * 0.34} ${-r * 0.08} L0 ${-r * 0.42} L${r * 0.34} ${-r * 0.08}`}
      />
    </g>
  );
}

/** Draws each stroke with the very line it is explaining. */
function LegendItem(props: {
  kind: "route" | "idle" | "dial" | "down";
  label: string;
}) {
  const cls = [
    "swarm-edge",
    props.kind === "idle" ? "is-alternate" : "",
    props.kind === "down" ? "is-down" : "",
    props.kind === "dial" ? "is-dialled" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <li className="swarm-legend-item">
      <svg className="swarm-legend-mark" viewBox="0 0 42 12" aria-hidden="true">
        <path className={cls} d="M2 6 H40" />
      </svg>
      <span>{props.label}</span>
    </li>
  );
}

/**
 * The halo class is the only thing that changes when work starts or stops, so
 * a poll that reports the same state leaves the element untouched and its
 * animation keeps its phase instead of jumping back to the first frame.
 */
function haloClass(state: WorkState): string {
  return state === "idle" ? "swarm-node-halo" : `swarm-node-halo is-${state}`;
}

/** A person has to answer before anything else moves, so waiting wins. */
function stateOf(work: NodeActivity | null): WorkState {
  if (!work || work.sessions === 0) {
    return "idle";
  }
  if (work.waiting > 0) {
    return "waiting";
  }
  return work.running > 0 ? "running" : "idle";
}

/** Every hop on the way to a node that has a turn in flight. */
function busyRoutes(
  layout: TopologyLayout,
  activity: Record<string, NodeActivity>,
): ReadonlySet<string> {
  const out = new Set<string>();
  for (const [route, work] of Object.entries(activity)) {
    if (work.running > 0 && work.waiting === 0) {
      for (const id of routeEdgeIds(layout, route)) {
        out.add(id);
      }
    }
  }
  return out;
}

/** "You are on X, reached through a, b." Empty when we are on the relay. */
function hereSentence(
  layout: TopologyLayout,
  current: string,
  t: (key: string, params?: Record<string, string | number>) => string,
): string {
  if (!current) {
    return "";
  }
  const node = layout.nodes.find(
    (n) => n.path.length > 0 && n.path.join("/") === current,
  );
  if (!node) {
    return "";
  }
  return t("swarm.graph.hereIs", {
    node: node.name,
    route: node.path.join(", "),
  });
}

/** Names the nodes in one work state, for the paragraph a screen reader gets. */
function namesSentence(
  layout: TopologyLayout,
  activity: Record<string, NodeActivity>,
  field: "running" | "waiting",
  say: (names: string) => string,
): string {
  const names = layout.nodes
    .filter(
      (n) =>
        n.path.length > 0 && (activity[n.path.join("/")]?.[field] ?? 0) > 0,
    )
    .map((n) => n.name);
  return names.length > 0 ? say(names.join(", ")) : "";
}

/** Paint order: a dead or optional wire first, the path in use over everything. */
function weight(
  e: PlacedEdge,
  live: ReadonlySet<string>,
  preview: ReadonlySet<string>,
  busy: ReadonlySet<string>,
): number {
  if (live.has(e.id)) {
    return 5;
  }
  if (preview.has(e.id)) {
    return 4;
  }
  if (busy.has(e.id)) {
    return 3;
  }
  if (!e.to.online) {
    return 0;
  }
  return e.alternate ? 1 : 2;
}

/**
 * SVG cannot measure text before it paints, and measuring after paint would
 * make every chip jump on the next poll. The advance of the UI font is close
 * enough to 0.55em to size a plate from the string itself.
 */
function chipWidth(text: string, fontSize: number, pad: number): number {
  return Math.round(textWidth(text, fontSize) + pad * 2);
}

function textWidth(text: string, fontSize: number): number {
  return Math.round(text.length * fontSize * 0.55);
}

/** SVG text has no ellipsis, so a long name is cut where the card ends. */
function clip(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, Math.max(max - 1, 1))}…`;
}

/**
 * Roughly how wide a string will be, so a plate can be given the text that
 * fits it. Counting characters instead would clip Russian early: Cyrillic
 * lowercase is narrower here than the Latin average a per-character budget is
 * calibrated on.
 */
function clipToWidth(text: string, px: number, size: number): string {
  const advance = (ch: string): number =>
    /[MWmw@%]/.test(ch)
      ? size * 0.82
      : /[ .,:;·'`|!iIjlt()[\]-]/.test(ch)
        ? size * 0.3
        : /[A-ZА-ЯЁ0-9]/.test(ch)
          ? size * 0.6
          : size * 0.5;
  let w = 0;
  let out = "";
  for (const ch of text) {
    w += advance(ch);
    if (w > px) {
      return `${out.slice(0, Math.max(out.length - 1, 1))}…`;
    }
    out += ch;
  }
  return out;
}
