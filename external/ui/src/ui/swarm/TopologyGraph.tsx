import { useId, useMemo } from "react";
import {
  NODE_METRICS as M,
  connectorFor,
  layoutTopology,
  type PlacedEdge,
  type PlacedNode,
  type TierRow,
} from "./layout";
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

/**
 * Draws the swarm as a network: the relay you are attached to on the top row,
 * then whatever it reaches a hop lower, then whatever that reaches. A spine
 * down the left counts the hops, relays are cards carrying a router mark, and
 * agents are circles with their name on a chip below.
 *
 * Hand-rolled SVG rather than a graph library: the arrangement and the wire
 * geometry are pure functions tested on their own, and the drawing is a few
 * dozen elements.
 */
export function TopologyGraph(props: {
  topology: SwarmTopology;
  selectedNode?: string | null;
  onPickNode?: (node: PlacedNode) => void;
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
  const pick = props.onPickNode;
  const { width, height, spineX } = layout;

  // Degree, not out-degree: "2 links" should count every wire the node carries.
  const degrees = new Map<string, number>();
  for (const e of layout.edges) {
    degrees.set(e.from.uuid, (degrees.get(e.from.uuid) ?? 0) + 1);
    degrees.set(e.to.uuid, (degrees.get(e.to.uuid) ?? 0) + 1);
  }

  const tierName = (row: TierRow): string => {
    if (!row.reachable) {
      return t("swarm.state.noRoute");
    }
    return row.depth === 0
      ? t("swarm.tier.here")
      : tp("swarm.tier.hop", row.depth);
  };

  const metaOf = (n: PlacedNode): string => {
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

  // role="img" collapses the subtree, so every per-node title and button role
  // inside is announced as nothing. This paragraph is the picture in words.
  const summary = layout.tiers
    .map((row) =>
      t("swarm.graph.tierNodes", {
        tier: tierName(row),
        names: layout.nodes
          .filter((n) => n.depth === row.depth)
          .map((n) => n.name)
          .join(", "),
      }),
    )
    .join(" ");

  // A way round and a dead link go down first, so a route in use always paints
  // over them; the picked node goes last so nothing overlaps its ring.
  const edges = [...layout.edges].sort(
    (a, b) => weight(a, props.selectedNode) - weight(b, props.selectedNode),
  );
  const nodes = [...layout.nodes].sort(
    (a, b) =>
      Number(isPicked(a, props.selectedNode)) -
      Number(isPicked(b, props.selectedNode)),
  );
  const first = layout.tiers[0];
  const last = layout.tiers[layout.tiers.length - 1];

  return (
    <div className="swarm-graph-panel">
      <p id={descId} className="swarm-graph-summary">
        {summary}
      </p>
      <div className="swarm-graph-scroll">
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
          <g className="swarm-graph-edges">
            {edges.map((e, i) => (
              <Wire
                key={`${e.from.uuid}-${e.to.uuid}-${e.name}-${i}`}
                edge={e}
                prefix={heads}
                title={`${e.name} · ${t(
                  e.alternate ? "swarm.state.wayRound" : "swarm.state.route",
                )}`}
              />
            ))}
          </g>

          <g className="swarm-graph-nodes">
            {nodes.map((n) => (
              <Node
                key={n.uuid}
                node={n}
                meta={metaOf(n)}
                picked={isPicked(n, props.selectedNode)}
                {...(pick ? { onPick: pick } : {})}
              />
            ))}
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
function Wire(props: { edge: PlacedEdge; prefix: string; title: string }) {
  const e = props.edge;
  const c = connectorFor(e);
  const dead = !e.to.online;
  const dials = e.to.transport === "tunnel";
  const cls = [
    "swarm-edge",
    e.alternate ? "is-alternate" : "",
    dead ? "is-down" : "",
    dials ? "is-dialled" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const head = dead ? "down" : e.alternate ? "idle" : "route";
  const label = clip(e.name, 16);
  const plate = chipWidth(label, 10, 9);
  return (
    <g className={c.peer ? "swarm-edge-group is-peer" : "swarm-edge-group"}>
      <title>{props.title}</title>
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
  picked: boolean;
  onPick?: (node: PlacedNode) => void;
}) {
  const n = props.node;
  const pick = props.onPick;
  const cls = [
    "swarm-node",
    n.kind === "relay" ? "swarm-node-relay" : "swarm-node-agent",
    n.depth === 0 ? "is-root" : "",
    n.online ? "is-online" : "is-offline",
    n.depth > 0 && n.path.length === 0 ? "is-stranded" : "",
    props.picked ? "is-selected" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <g
      className={cls}
      transform={`translate(${n.x},${n.y})`}
      onClick={() => pick?.(n)}
      role={pick ? "button" : undefined}
      tabIndex={pick ? 0 : undefined}
      onKeyDown={
        pick
          ? (ev) => {
              if (ev.key === "Enter" || ev.key === " ") {
                ev.preventDefault();
                pick(n);
              }
            }
          : undefined
      }
    >
      <title>{`${n.name} · ${props.meta}`}</title>
      {n.kind === "relay" ? (
        <RelayCard node={n} meta={props.meta} />
      ) : (
        <AgentDisc node={n} meta={props.meta} />
      )}
    </g>
  );
}

/** A relay routes: a soft card with the router mark on an accent tile. */
function RelayCard(props: { node: PlacedNode; meta: string }) {
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
        {clip(n.name, Math.floor(RELAY_TEXT_W / 6.6))}
      </text>
      <text
        className="swarm-node-meta"
        x={TEXT_X}
        y={M.relayMetaDrop}
        textAnchor="start"
      >
        {clip(props.meta, Math.floor(RELAY_TEXT_W / 5.5))}
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
function AgentDisc(props: { node: PlacedNode; meta: string }) {
  const n = props.node;
  const r = M.agentRadius;
  const label = clip(n.name, 16);
  const chip = chipWidth(label, 11, 10);
  return (
    <>
      <circle className="swarm-node-hit" r={r + 12} />
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

/** Selection travels as a route string, the same key the filter chips use. */
function isPicked(n: PlacedNode, selected?: string | null): boolean {
  return selected === n.path.join("/");
}

/** Paint order: a dead or optional wire first, the route in use over it. */
function weight(e: PlacedEdge, selected?: string | null): number {
  if (isPicked(e.to, selected)) {
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
  return Math.round(text.length * fontSize * 0.55 + pad * 2);
}

/** SVG text has no ellipsis, so a long name is cut where the card ends. */
function clip(text: string, max: number): string {
  return text.length <= max ? text : `${text.slice(0, Math.max(max - 1, 1))}…`;
}
