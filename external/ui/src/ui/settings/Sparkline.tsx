const W = 240;
const H = 46;
const PAD = 2;

/**
 * A line chart as one inline SVG, no library: a polyline for each run of
 * points and a gap for each missing one (null). Several lines of one chart
 * share a scale by sharing `max`; only the first is an image, the rest are
 * drawn over it and left to the label of the first. `step` draws a gauge as
 * stairs.
 */
export function Sparkline(props: {
  values: (number | null)[];
  label: string;
  latest: string;
  max?: number;
  tone?: string;
  step?: boolean;
  hidden?: boolean;
}) {
  const { values, step } = props;
  const top =
    props.max ?? Math.max(0, ...values.filter((v): v is number => v !== null));
  const x = (i: number) =>
    values.length > 1 ? (i * W) / (values.length - 1) : W / 2;
  const y = (v: number) => H - PAD - (top > 0 ? (v / top) * (H - 2 * PAD) : 0);
  const runs: string[] = [];
  let run: string[] = [];
  let before: number | null = null;
  const end = () => {
    if (run.length > 0) {
      // One point alone is a dot: the line's round caps draw it.
      runs.push((run.length === 1 ? [...run, ...run] : run).join(" "));
    }
    run = [];
    before = null;
  };
  values.forEach((v, i) => {
    if (v === null) {
      end();
      return;
    }
    if (step && before !== null) {
      run.push(`${x(i).toFixed(1)},${y(before).toFixed(1)}`);
    }
    run.push(`${x(i).toFixed(1)},${y(v).toFixed(1)}`);
    before = v;
  });
  end();
  return (
    <svg
      className={`system-spark system-spark--${props.tone ?? "accent"}`}
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      {...(props.hidden
        ? { "aria-hidden": true }
        : { role: "img", "aria-label": `${props.label}: ${props.latest}` })}
    >
      {runs.map((points, i) => (
        <polyline key={i} points={points} fill="none" />
      ))}
    </svg>
  );
}
