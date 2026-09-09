import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  NODE_METRICS as M,
  connectorFor,
  layoutTopology,
  routeEdgeIds,
  type PlacedEdge,
  type PlacedNode,
  type TierRow,
  type TopologyLayout,
} from "./layout";
import type { NodeActivity } from "./routes";
import type { SwarmTopology } from "./types";
import { useT } from "../i18n/I18nProvider";

const RELAY_HALF_W = M.relayWidth / 2;
const RELAY_HALF_H = M.relayHeight / 2;
/** Left edge of the accent tile, and everything the relay card hangs off it. */
const TILE_X = -RELAY_HALF_W + M.tileInset;
const TILE_CX = TILE_X + M.tile / 2;
const TEXT_X = TILE_X + M.tile + M.textGap;
/** Room the name has before it would run under the status dot. */
const RELAY_TEXT_W = RELAY_HALF_W - M.statusInset - 4 - TEXT_X;

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
  /** Work per node, keyed by joined route. */
  activity?: Record<string, NodeActivity>;
  onEnterNode?: (node: PlacedNode) => void;
}) {
  const { t, tp } = useT();
  // SwarmView re-polls every five seconds; without this every poll rebuilds the
  // arrangement even when nothing about the swarm moved.
  const layout = useMemo(
    () => layoutTopology(props.topology),
    [props.topology],
  );
  // Two graphs on one page would otherwise share marker ids and the second one
  // would repaint the first one's arrowheads. useId can contain colons, which
  // read badly inside url(#…), so only id-safe characters survive.
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const heads = `swarm-head-${uid}`;
  const descId = `swarm-desc-${uid}`;
  const enter = props.onEnterNode;
  const activity = props.activity ?? NO_ACTIVITY;
  const current = props.currentNode || "";
  const { width, height, spineX } = layout;

  // Previewing a route on hover needs no data, only which node the pointer is
  // over. Focus feeds the same state so the keyboard sees what the mouse does.
  const [preview, setPreview] = useState<string>("");

  const scroller = useRef<HTMLDivElement | null>(null);

  // Degree, not out-degree: "2 links" should count every wire the node carries.
  const degrees = new Map<string, number>();
  for (const e of layout.edges) {
    degrees.set(e.from.uuid, (degrees.get(e.from.uuid) ?? 0) + 1);
    degrees.set(e.to.uuid, (degrees.get(e.to.uuid) ?? 0) + 1);
  }

  const liveEdges = useMemo(
    () => (current ? routeEdgeIds(layout, current) : NO_EDGES),
    [layout, current],
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

  // A swarm wider than the box opens on its left gutter, which on a phone is
  // an empty margin. Start where the reader is instead - the node the app is
  // on, or the relay everything hangs off. Keyed to the width and to that
  // node, so a poll that changes neither never yanks the view back.
  const focusX = useMemo(() => {
    const node =
      layout.nodes.find(
        (n) => current && n.path.length > 0 && n.path.join("/") === current,
      ) ?? layout.nodes.find((n) => n.depth === 0);
    return node ? node.x : 0;
  }, [layout, current]);

  useEffect(() => {
    const box = scroller.current;
    if (!box || box.scrollWidth <= box.clientWidth) {
      return;
    }
    box.scrollLeft = focusX - box.clientWidth / 2;
  }, [focusX, width]);

  const tierName = (row: TierRow): string => {
    if (!row.reachable) {
      return t("swarm.state.noRoute");
    }
    return row.depth === 0
      ? t("swarm.tier.attached")
      : tp("swarm.tier.hop", row.depth);
  };

  const metaOf = (n: PlacedNode): string => {
    // Where the app is now beats what the node is: the shape already says
    // whether it routes or works, and only one node can say this.
    if (current && n.path.length > 0 && n.path.join("/") === current) {
      return t("swarm.node.here");
    }
    if (n.depth > 0 && n.path.length === 0) {
      return t("swarm.state.noRoute");
    }
    if (!n.online) {
      return t("swarm.state.offline");
    }
    if (n.kind !== "relay") {
      return n.transport === "tunnel"
        ? t("swarm.state.dialsOut")
        : t("swarm.state.agent");
    }
    // A relay's line says what it is and how much it carries. That it dials
    // out is already on the card as a badge, and spelling it out here only
    // pushed the useful half off the end of the plate.
    const links = degrees.get(n.uuid) ?? 0;
    return links > 0
      ? `${t("swarm.state.relay")} · ${tp("swarm.node.links", links)}`
      : t("swarm.state.relay");
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

  // role="img" collapses the subtree, so every per-node title and button role
  // inside is announced as nothing. This paragraph is the picture in words, and
  // it has to carry the live half of it too.
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

  const enterable = useCallback(
    (n: PlacedNode): boolean => !!enter && n.depth > 0 && n.path.length > 0,
    [enter],
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

  return (
    <div className="swarm-graph-panel">
      <p id={descId} className="swarm-graph-summary">
        {summary}
      </p>
      <div className="swarm-graph-scroll" ref={scroller}>
        <svg
          className="swarm-graph"
          viewBox={`0 0 ${width} ${height}`}
          width={width}
          height={height}
          role="img"
          aria-label={t("swarm.graph.aria")}
          aria-describedby={descId}
        >
          <ArrowDefs prefix={heads} />

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

          {/* A wire crossing a node must never swallow a click meant for it. */}
          <g
            className={`swarm-graph-edges${tracing ? " is-tracing" : ""}`}
            aria-hidden="true"
          >
            {edges.map((e) => (
              <Wire
                key={e.id}
                edge={e}
                prefix={heads}
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
                  work={workLine(work)}
                  state={stateOf(work)}
                  current={!!current && n.path.join("/") === current}
                  onRoute={onRoute.has(n.uuid)}
                  {...(enterable(n)
                    ? {
                        onEnter: enter,
                        enterLabel: t("swarm.graph.enter", { node: n.name }),
                      }
                    : {})}
                  onPreview={setPreview}
                />
              );
            })}
          </g>
        </svg>
      </div>

      {/* HTML rather than SVG so it wraps on a phone instead of setting a
          minimum width the graph would then have to scroll to. */}
      <ul className="swarm-graph-legend" aria-label={t("swarm.graph.legend")}>
        <LegendItem
          prefix={heads}
          kind="route"
          label={t("swarm.state.route")}
        />
        <LegendItem
          prefix={heads}
          kind="idle"
          label={t("swarm.state.wayRound")}
        />
        <LegendItem
          prefix={heads}
          kind="dial"
          label={t("swarm.state.dialsOut")}
        />
        <LegendItem
          prefix={heads}
          kind="down"
          label={t("swarm.state.offline")}
        />
      </ul>
    </div>
  );
}

/**
 * One marker per link state. A marker resolves its paint where it sits, in
 * defs, so it can never inherit the stroke of the path referencing it: each
 * head has to name its own token or it stops following the theme.
 */
function ArrowDefs(props: { prefix: string }) {
  const p = props.prefix;
  return (
    <defs>
      <marker
        id={`${p}-route`}
        viewBox="0 0 12 12"
        refX={11}
        refY={6}
        markerWidth={12}
        markerHeight={12}
        markerUnits="userSpaceOnUse"
        orient="auto"
      >
        <path className="swarm-head-route" d="M1.5 1.9 L11 6 L1.5 10.1 Z" />
      </marker>
      <marker
        id={`${p}-idle`}
        viewBox="0 0 12 12"
        refX={11}
        refY={6}
        markerWidth={12}
        markerHeight={12}
        markerUnits="userSpaceOnUse"
        orient="auto"
      >
        <path className="swarm-head-idle" d="M3 2.4 L10.6 6 L3 9.6" />
      </marker>
      <marker
        id={`${p}-down`}
        viewBox="0 0 12 12"
        refX={11}
        refY={6}
        markerWidth={12}
        markerHeight={12}
        markerUnits="userSpaceOnUse"
        orient="auto"
      >
        <path className="swarm-head-down" d="M3 2.4 L10.6 6 L3 9.6" />
      </marker>
      {/* Sits at the near end of a dialled link and points back the way it
          came: that node opened the connection, so it reaches inwards even
          though the relay's reach runs outwards. */}
      <marker
        id={`${p}-dial`}
        viewBox="0 0 10 10"
        refX={0}
        refY={5}
        markerWidth={10}
        markerHeight={10}
        markerUnits="userSpaceOnUse"
        orient="auto-start-reverse"
      >
        <path className="swarm-head-dial" d="M0 0.6 L5.6 5 L0 9.4 Z" />
      </marker>
    </defs>
  );
}

/** A link, its state, and the name a route would call it by. */
function Wire(props: {
  edge: PlacedEdge;
  prefix: string;
  live: boolean;
  preview: boolean;
  busy: boolean;
}) {
  const e = props.edge;
  const c = connectorFor(e);
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
  const head = dead ? "down" : e.alternate ? "idle" : "route";
  const label = clip(e.name, 16);
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
      <path
        className={cls}
        d={c.d}
        markerEnd={`url(#${props.prefix}-${head})`}
        {...(dials ? { markerStart: `url(#${props.prefix}-dial)` } : {})}
      />
      <g
        className="swarm-edge-chip"
        transform={`translate(${c.labelX},${c.labelY})`}
      >
        <rect x={-plate / 2} y={-8} width={plate} height={16} rx={8} />
        <text className="swarm-edge-label" y={3} textAnchor="middle">
          {label}
        </text>
      </g>
    </g>
  );
}

function Node(props: {
  node: PlacedNode;
  meta: string;
  /** Empty when nothing is running there and nothing is parked there. */
  work: string;
  state: WorkState;
  current: boolean;
  onRoute: boolean;
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
    n.depth === 0 ? "is-root" : "",
    n.online ? "is-online" : "is-offline",
    n.depth > 0 && n.path.length === 0 ? "is-stranded" : "",
    props.current ? "is-current" : "",
    props.onRoute ? "is-on-route" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const title = [n.name, props.meta, props.work, props.enterLabel]
    .filter(Boolean)
    .join(" · ");
  return (
    <g
      className={cls}
      transform={`translate(${n.x},${n.y})`}
      onClick={enter ? () => enter(n) : undefined}
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
        <RelayCard node={n} meta={props.meta} state={props.state} />
      ) : (
        <AgentDisc node={n} meta={props.meta} state={props.state} />
      )}
      {props.work ? (
        <WorkLine
          text={props.work}
          state={props.state}
          y={n.kind === "relay" ? M.relayActivityDrop : M.agentActivityDrop}
        />
      ) : null}
    </g>
  );
}

/** A relay routes: a soft card with the router mark on an accent tile. */
function RelayCard(props: {
  node: PlacedNode;
  meta: string;
  state: WorkState;
}) {
  const n = props.node;
  return (
    <>
      <rect
        className="swarm-node-hit"
        x={-RELAY_HALF_W - 10}
        y={-RELAY_HALF_H - 12}
        width={M.relayWidth + 20}
        height={M.relayHeight + 24}
        rx={M.relayRadius + 8}
      />
      <rect
        className={haloClass(props.state)}
        x={-RELAY_HALF_W - 9}
        y={-RELAY_HALF_H - 9}
        width={M.relayWidth + 18}
        height={M.relayHeight + 18}
        rx={M.relayRadius + 9}
      />
      {/* Elevation without a filter: a darker plate peeking out below the card.
          It costs one shape and reads as a shadow on light and dark alike. */}
      <rect
        className="swarm-node-shadow"
        x={-RELAY_HALF_W + 5}
        y={-RELAY_HALF_H + 4}
        width={M.relayWidth - 10}
        height={M.relayHeight}
        rx={M.relayRadius}
      />
      <rect
        className="swarm-node-ring"
        x={-RELAY_HALF_W - 6}
        y={-RELAY_HALF_H - 6}
        width={M.relayWidth + 12}
        height={M.relayHeight + 12}
        rx={M.relayRadius + 6}
      />
      <rect
        className="swarm-node-body"
        x={-RELAY_HALF_W}
        y={-RELAY_HALF_H}
        width={M.relayWidth}
        height={M.relayHeight}
        rx={M.relayRadius}
      />
      <rect
        className="swarm-node-tile"
        x={TILE_X}
        y={-M.tile / 2}
        width={M.tile}
        height={M.tile}
        rx={M.tileRadius}
      />
      <RouterMark cx={TILE_CX} />
      <text
        className="swarm-node-label"
        x={TEXT_X}
        y={M.relayNameDrop}
        textAnchor="start"
      >
        {clipToWidth(n.name, RELAY_TEXT_W, 12.5)}
      </text>
      <text
        className="swarm-node-meta"
        x={TEXT_X}
        y={M.relayMetaDrop}
        textAnchor="start"
      >
        {clipToWidth(props.meta, RELAY_TEXT_W, 10.5)}
      </text>
      <StatusDot
        x={RELAY_HALF_W - M.statusInset}
        y={-RELAY_HALF_H + M.statusInset}
        online={n.online}
      />
      {n.transport === "tunnel" ? (
        <DialBadge x={-RELAY_HALF_W + M.badgeRadius + 4} y={RELAY_HALF_H} />
      ) : null}
    </>
  );
}

/** An agent does the work: a circle round a prompt, its name on a chip below. */
function AgentDisc(props: {
  node: PlacedNode;
  meta: string;
  state: WorkState;
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
      <g className="swarm-node-glyph">
        <path
          d={`M${-0.327 * r} ${-0.231 * r} L${-0.096 * r} 0 L${-0.327 * r} ${0.231 * r}`}
        />
        <path d={`M${0.058 * r} ${0.25 * r} H${0.346 * r}`} />
      </g>
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
      <text className="swarm-node-meta" y={M.agentMetaDrop} textAnchor="middle">
        {clip(props.meta, 22)}
      </text>
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

/** Draws each stroke with the very marker it is explaining. */
function LegendItem(props: {
  prefix: string;
  kind: "route" | "idle" | "dial" | "down";
  label: string;
}) {
  const dial = props.kind === "dial";
  const cls = [
    "swarm-edge",
    props.kind === "idle" ? "is-alternate" : "",
    props.kind === "down" ? "is-down" : "",
    dial ? "is-dialled" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const head = dial ? "route" : props.kind;
  return (
    <li className="swarm-legend-item">
      <svg className="swarm-legend-mark" viewBox="0 0 42 12" aria-hidden="true">
        <ArrowDefs prefix={`${props.prefix}-lg-${props.kind}`} />
        <path
          className={cls}
          d="M2 6 H30"
          markerEnd={`url(#${props.prefix}-lg-${props.kind}-${head})`}
          {...(dial
            ? { markerStart: `url(#${props.prefix}-lg-${props.kind}-dial)` }
            : {})}
        />
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
