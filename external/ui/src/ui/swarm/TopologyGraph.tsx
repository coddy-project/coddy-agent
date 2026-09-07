import { layoutTopology, type PlacedNode } from "./layout";
import type { SwarmTopology } from "./types";

/**
 * Draws the swarm as a tier per hop: the relay you are attached to on top, then
 * whatever it reaches, then whatever that reaches.
 *
 * Hand-rolled SVG rather than a graph library: the arrangement is a pure
 * function tested on its own, and the drawing is a few dozen elements.
 */
export function TopologyGraph(props: {
  topology: SwarmTopology;
  selectedNode?: string | null;
  onPickNode?: (node: PlacedNode) => void;
}) {
  const layout = layoutTopology(props.topology);
  const { width, height } = layout;

  return (
    <div className="swarm-graph-scroll">
      <svg
        className="swarm-graph"
        viewBox={`0 0 ${width} ${height}`}
        width={width}
        height={height}
        role="img"
        aria-label="Swarm topology"
      >
        <g className="swarm-graph-edges">
          {layout.edges.map((e, i) => {
            const key = `${e.from.uuid}-${e.to.uuid}-${e.name}-${i}`;
            return (
              <g key={key}>
                <line
                  x1={e.from.x}
                  y1={e.from.y + 18}
                  x2={e.to.x}
                  y2={e.to.y - 18}
                  className={
                    e.alternate
                      ? "swarm-edge swarm-edge-alternate"
                      : "swarm-edge"
                  }
                />
                <text
                  x={(e.from.x + e.to.x) / 2}
                  y={(e.from.y + e.to.y) / 2}
                  className="swarm-edge-label"
                  textAnchor="middle"
                >
                  {e.name}
                </text>
              </g>
            );
          })}
        </g>
        <g className="swarm-graph-nodes">
          {layout.nodes.map((n) => {
            const selected = props.selectedNode === n.path.join("/");
            const classes = [
              "swarm-node",
              n.kind === "relay" ? "swarm-node-relay" : "swarm-node-agent",
              n.online ? "is-online" : "is-offline",
              selected ? "is-selected" : "",
            ]
              .filter(Boolean)
              .join(" ");
            return (
              <g
                key={n.uuid}
                className={classes}
                transform={`translate(${n.x},${n.y})`}
                onClick={() => props.onPickNode?.(n)}
                role={props.onPickNode ? "button" : undefined}
                tabIndex={props.onPickNode ? 0 : undefined}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault();
                    props.onPickNode?.(n);
                  }
                }}
              >
                {n.kind === "relay" ? (
                  <rect x={-52} y={-18} width={104} height={36} rx={8} />
                ) : (
                  <circle cx={0} cy={0} r={18} />
                )}
                <text
                  className="swarm-node-label"
                  y={n.kind === "relay" ? 5 : 36}
                  textAnchor="middle"
                >
                  {n.name}
                </text>
                {n.transport === "tunnel" ? (
                  <title>{`${n.name} — dials out (tunnel)`}</title>
                ) : (
                  <title>{n.name}</title>
                )}
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
}
