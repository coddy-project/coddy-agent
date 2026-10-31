export function activate(coddy) {
  coddy.i18n.registerMessages("en", {
    title: "Theme Lab",
    reset: "Reset",
    note: "Overrides last until the extension is disabled or the page reloads.",
    shiftAll: "Shift every colour",
    hue: "Hue",
    saturation: "Saturation",
    lightness: "Lightness",
    tokens: "Tokens",
  });
  coddy.i18n.registerMessages("ru", {
    title: "Лаборатория тем",
    reset: "Сбросить",
    note: "Изменения цветов действуют, пока расширение не отключат или страницу не перезагрузят.",
    shiftAll: "Сдвинуть все цвета",
    hue: "Тон",
    saturation: "Насыщенность",
    lightness: "Светлота",
    tokens: "Токены",
  });
  const channels = (text, max) => {
    const read = (part) =>
      part.endsWith("%")
        ? (parseFloat(part) / 100) * 255
        : (parseFloat(part) / max) * 255;
    const [r, g, b] = text.split(/[\s,/]+/).filter(Boolean).map(read);
    return { r: r, g: g, b: b };
  };
  const parse = (text) => {
    const value = String(text).trim().toLowerCase();
    let match = value.match(/^#([0-9a-f]{3})$/);
    if (match)
      return {
        r: parseInt(match[1][0] + match[1][0], 16),
        g: parseInt(match[1][1] + match[1][1], 16),
        b: parseInt(match[1][2] + match[1][2], 16),
      };
    match = value.match(/^#([0-9a-f]{6})$/);
    if (match)
      return {
        r: parseInt(match[1].slice(0, 2), 16),
        g: parseInt(match[1].slice(2, 4), 16),
        b: parseInt(match[1].slice(4, 6), 16),
      };
    match = value.match(/^rgba?\(([^)]+)\)$/);
    if (match) return channels(match[1], 255);
    match = value.match(/^color\(srgb\s+([^)]+)\)$/);
    if (match) return channels(match[1], 1);
    return null;
  };
  const toHex = (rgb) => {
    const part = (v) => {
      const n = Math.max(0, Math.min(255, Math.round(v)));
      return (n < 16 ? "0" : "") + n.toString(16);
    };
    return "#" + part(rgb.r) + part(rgb.g) + part(rgb.b);
  };
  const toHsl = (rgb) => {
    if (!rgb) return { h: 0, s: 0, l: 50 };
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
    return { h: Math.round(h), s: Math.round(s * 100), l: Math.round(l * 100) };
  };
  const hslValue = (t) => "hsl(" + t.h + ", " + t.s + "%, " + t.l + "%)";

  // The app repaints from its whole token layer. The lab seeds every
  // colour-valued token from `theme.tokens()` and shifts them all at once,
  // with a colour input per token for the fine moves.
  let base = {};
  let tones = {};
  let custom = {};
  const shift = { hue: 0, sat: 100, light: 0 };
  let handle = null;
  const seed = () => {
    base = coddy.theme.tokens();
    tones = {};
    custom = {};
    for (const name of Object.keys(base)) {
      const hsl = toHsl(parse(base[name]));
      if (parse(base[name])) tones[name] = hsl;
    }
    shift.hue = 0;
    shift.sat = 100;
    shift.light = 0;
  };
  seed();
  const build = () => {
    const out = {};
    for (const name of Object.keys(tones)) {
      if (custom[name]) out[name] = custom[name];
      else {
        const t = tones[name];
        out[name] = hslValue({
          h: (t.h + shift.hue + 360) % 360,
          s: Math.max(0, Math.min(100, Math.round((t.s * shift.sat) / 100))),
          l: Math.max(0, Math.min(100, t.l + shift.light)),
        });
      }
    }
    return out;
  };
  const apply = () => {
    const out = build();
    if (handle) handle.update(out);
    else handle = coddy.theme.override(out);
  };

  const panel = coddy.ui.registerPanel({
    id: "theme-lab",
    title: coddy.i18n.t("title"),
    placement: "slide-over",
    mount(element) {
      const paint = () => {
        element.replaceChildren();
        const box = document.createElement("section");
        box.className = "ext-theme-lab";

        const heading = document.createElement("h3");
        heading.textContent = coddy.i18n.t("shiftAll");
        box.append(heading);
        const shifters = [
          { key: "hue", min: -180, max: 180 },
          { key: "saturation", min: 0, max: 200 },
          { key: "lightness", min: -50, max: 50 },
        ];
        for (const spec of shifters) {
          const row = document.createElement("div");
          row.className = "ext-theme-lab-row";
          const label = document.createElement("label");
          label.textContent = coddy.i18n.t(spec.key);
          const slider = document.createElement("input");
          slider.type = "range";
          slider.min = String(spec.min);
          slider.max = String(spec.max);
          const current =
            spec.key === "hue"
              ? shift.hue
              : spec.key === "saturation"
                ? shift.sat
                : shift.light;
          slider.value = String(current);
          slider.addEventListener(
            "input",
            () => {
              const value = Number(slider.value);
              if (spec.key === "hue") shift.hue = value;
              else if (spec.key === "saturation") shift.sat = value;
              else shift.light = value;
              apply();
            },
            { signal: coddy.signal },
          );
          row.append(label, slider);
          box.append(row);
        }

        const tokensHead = document.createElement("h3");
        tokensHead.textContent = coddy.i18n.t("tokens");
        box.append(tokensHead);
        for (const name of Object.keys(tones)) {
          const row = document.createElement("div");
          row.className = "ext-theme-lab-row ext-theme-lab-token-row";
          const label = document.createElement("code");
          label.textContent = name;
          const picker = document.createElement("input");
          picker.type = "color";
          picker.value = toHex(parse(base[name]));
          picker.addEventListener(
            "input",
            () => {
              custom[name] = picker.value;
              apply();
            },
            { signal: coddy.signal },
          );
          row.append(label, picker);
          box.append(row);
        }

        const reset = document.createElement("button");
        reset.className = "settings-btn";
        reset.textContent = coddy.i18n.t("reset");
        reset.addEventListener(
          "click",
          () => {
            if (handle) {
              handle.remove();
              handle = null;
            }
            seed();
            paint();
          },
          { signal: coddy.signal },
        );
        const note = document.createElement("p");
        note.className = "ext-theme-lab-note";
        note.textContent = coddy.i18n.t("note");
        box.append(reset, note);
        element.append(box);
      };
      paint();
      const watched = coddy.i18n.onChange(paint);
      return () => {
        watched.remove();
        element.replaceChildren();
      };
    },
  });
  const item = coddy.ui.registerMenuItem({
    id: "open-theme-lab",
    title: coddy.i18n.t("title"),
    onSelect: () => coddy.ui.openPanel("theme-lab"),
  });
  const watched = coddy.i18n.onChange(() => {
    panel.update({ title: coddy.i18n.t("title") });
    item.update({ title: coddy.i18n.t("title") });
  });
  return () => {
    watched.remove();
    item.remove();
    panel.remove();
    if (handle) {
      handle.remove();
      handle = null;
    }
  };
}
