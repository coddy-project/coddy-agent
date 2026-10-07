/** Dual-ring context meter; outer arc only when fill > 0, from 12 o'clock. */
export function ContextUsageRing({
  fill01,
  size = 30,
}: {
  fill01: number;
  size?: number;
}) {
  const vb = 28;
  const cx = vb / 2;
  const rOuter = 12;
  const rInner = 7;
  const c = 2 * Math.PI * rOuter;
  const clamped = Math.min(1, Math.max(0, fill01));
  const off = c * (1 - clamped);
  const showOuter = clamped > 0;

  return (
    <div className="context-ring" role="img" aria-hidden="true">
      <svg
        viewBox={`0 0 ${vb} ${vb}`}
        width={size}
        height={size}
        aria-hidden="true"
      >
        <circle className="context-ring-inner" cx={cx} cy={cx} r={rInner} />
        {showOuter ? (
          <circle
            className="context-ring-fg"
            cx={cx}
            cy={cx}
            r={rOuter}
            strokeDasharray={c}
            strokeDashoffset={off}
          />
        ) : null}
      </svg>
    </div>
  );
}
