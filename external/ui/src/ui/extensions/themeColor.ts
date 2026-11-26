export type ThemeHSL = Readonly<{ h: number; s: number; l: number }>;
type RGB = { r: number; g: number; b: number };

export function parseThemeColor(
  value: string,
  doc?: Document,
): ThemeHSL | null {
  const direct = parseDirect(String(value).trim().toLowerCase());
  if (direct) return direct;
  const root = doc ?? (typeof document === "undefined" ? null : document);
  if (!root) return null;
  const probe = root.createElement("span");
  probe.style.display = "none";
  (root.body ?? root.documentElement).appendChild(probe);
  try {
    probe.style.color = String(value).trim();
    if (!probe.style.color) return null;
    const computed = (root.defaultView ?? window).getComputedStyle(probe).color;
    return parseDirect(computed.trim().toLowerCase());
  } finally {
    probe.remove();
  }
}

export function formatThemeColor(color: ThemeHSL): string {
  const h = ((Math.round(color.h) % 360) + 360) % 360;
  const s = Math.max(0, Math.min(100, Math.round(color.s)));
  const l = Math.max(0, Math.min(100, Math.round(color.l)));
  return `hsl(${h}, ${s}%, ${l}%)`;
}

function parseDirect(value: string): ThemeHSL | null {
  let match = /^#([0-9a-f]{3})$/.exec(value);
  if (match) {
    return rgbToHsl({
      r: parseInt(match[1]![0]! + match[1]![0], 16),
      g: parseInt(match[1]![1]! + match[1]![1], 16),
      b: parseInt(match[1]![2]! + match[1]![2], 16),
    });
  }
  match = /^#([0-9a-f]{6})(?:[0-9a-f]{2})?$/.exec(value);
  if (match) {
    return rgbToHsl({
      r: parseInt(match[1]!.slice(0, 2), 16),
      g: parseInt(match[1]!.slice(2, 4), 16),
      b: parseInt(match[1]!.slice(4, 6), 16),
    });
  }
  match = /^rgba?\(([^)]+)\)$/.exec(value);
  if (match) {
    const parts = components(match[1]!);
    if (parts.length >= 3) {
      return rgbToHsl({
        r: channel(parts[0]!, 255),
        g: channel(parts[1]!, 255),
        b: channel(parts[2]!, 255),
      });
    }
  }
  match = /^hsla?\(([^)]+)\)$/.exec(value);
  if (match) {
    const parts = components(match[1]!);
    if (
      parts.length >= 3 &&
      parts[1]!.endsWith("%") &&
      parts[2]!.endsWith("%")
    ) {
      return {
        h: hue(parseFloat(parts[0]!)),
        s: clamp(parseFloat(parts[1]!), 0, 100),
        l: clamp(parseFloat(parts[2]!), 0, 100),
      };
    }
  }
  match = /^color\(srgb\s+([^)]+)\)$/.exec(value);
  if (match) {
    const parts = components(match[1]!);
    if (parts.length >= 3) {
      return rgbToHsl({
        r: channel(parts[0]!, 1),
        g: channel(parts[1]!, 1),
        b: channel(parts[2]!, 1),
      });
    }
  }
  match = /^oklch\(([^)]+)\)$/.exec(value);
  if (match) {
    const parts = components(match[1]!);
    if (parts.length >= 3) return oklchToHsl(parts.map(Number) as number[]);
  }
  return null;
}

function components(value: string): string[] {
  return value
    .replace(/\//g, " ")
    .split(/[\s,]+/)
    .filter(Boolean);
}

function channel(value: string, max: number): number {
  return value.endsWith("%")
    ? (parseFloat(value) / 100) * 255
    : max === 255
      ? parseFloat(value)
      : parseFloat(value) * 255;
}

function hue(value: number): number {
  return ((value % 360) + 360) % 360;
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

function rgbToHsl(rgb: RGB): ThemeHSL {
  const r = rgb.r / 255;
  const g = rgb.g / 255;
  const b = rgb.b / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const delta = max - min;
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  if (delta) {
    s = l > 0.5 ? delta / (2 - max - min) : delta / (max + min);
    if (max === r) h = ((g - b) / delta + (g < b ? 6 : 0)) * 60;
    else if (max === g) h = ((b - r) / delta + 2) * 60;
    else h = ((r - g) / delta + 4) * 60;
  }
  return {
    h: Math.round(hue(h)),
    s: Math.round(s * 100),
    l: Math.round(l * 100),
  };
}

function oklchToHsl(parts: number[]): ThemeHSL | null {
  const [lightness, chroma, angle] = parts;
  if (![lightness, chroma, angle].every(Number.isFinite)) return null;
  const a = chroma! * Math.cos((angle! * Math.PI) / 180);
  const b = chroma! * Math.sin((angle! * Math.PI) / 180);
  const l = (lightness! + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (lightness! - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (lightness! - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return rgbToHsl({
    r: gamma(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s) * 255,
    g: gamma(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s) * 255,
    b: gamma(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s) * 255,
  });
}

function gamma(value: number): number {
  const linear = clamp(value, 0, 1);
  return linear <= 0.0031308
    ? 12.92 * linear
    : 1.055 * linear ** (1 / 2.4) - 0.055;
}
