export type GutterRow = {
  logicalLine: number;
  showNumber: boolean;
  visualIndex: number;
};

export function buildGutterRows(visualRows: number[]): GutterRow[] {
  const out: GutterRow[] = [];
  for (let logicalLine = 0; logicalLine < visualRows.length; logicalLine++) {
    const rows = Math.max(1, visualRows[logicalLine] ?? 1);
    for (let visualIndex = 0; visualIndex < rows; visualIndex++) {
      out.push({
        logicalLine,
        showNumber: visualIndex === 0,
        visualIndex,
      });
    }
  }
  return out;
}

export function lineHeightPxFromComputed(cs: CSSStyleDeclaration): number {
  const lh = parseFloat(cs.lineHeight);
  if (Number.isFinite(lh) && lh > 4) {
    return lh;
  }
  const fs = parseFloat(cs.fontSize);
  return Number.isFinite(fs) ? fs * 1.5 : 19.5;
}

export function textareaTextWidthPx(ta: HTMLTextAreaElement): number {
  const cs = getComputedStyle(ta);
  const pl = parseFloat(cs.paddingLeft) || 0;
  const pr = parseFloat(cs.paddingRight) || 0;
  return Math.max(0, ta.clientWidth - pl - pr);
}

export function applyMeasureProbeStyles(
  probe: HTMLElement,
  ta: HTMLTextAreaElement,
  textWidthPx: number,
): number {
  const cs = getComputedStyle(ta);
  probe.style.width = `${textWidthPx}px`;
  probe.style.font = cs.font;
  probe.style.fontSize = cs.fontSize;
  probe.style.lineHeight = cs.lineHeight;
  probe.style.fontFamily = cs.fontFamily;
  probe.style.letterSpacing = cs.letterSpacing;
  probe.style.whiteSpace = cs.whiteSpace;
  probe.style.overflowWrap = cs.overflowWrap;
  probe.style.wordBreak = cs.wordBreak;
  probe.style.boxSizing = "border-box";
  probe.style.padding = "0";
  probe.style.margin = "0";
  return lineHeightPxFromComputed(cs);
}

export function measureLineVisualRows(
  line: string,
  textWidthPx: number,
  lineHeightPx: number,
  probe: HTMLElement,
): number {
  if (textWidthPx <= 0 || lineHeightPx <= 0) {
    return 1;
  }
  probe.textContent = line.length > 0 ? line : "\u00a0";
  const h = probe.offsetHeight;
  return Math.max(1, Math.ceil((h - 1) / lineHeightPx));
}

export function measureAllVisualRows(
  lines: string[],
  textWidthPx: number,
  lineHeightPx: number,
  probe: HTMLElement,
): number[] {
  return lines.map((line) =>
    measureLineVisualRows(line, textWidthPx, lineHeightPx, probe),
  );
}

export function cursorLineIndex(text: string, selectionStart: number): number {
  if (selectionStart <= 0) return 0;
  return text.slice(0, selectionStart).split("\n").length - 1;
}
