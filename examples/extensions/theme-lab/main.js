export function activate(coddy) {
  coddy.i18n.registerMessages("en", {
    title: "Theme Lab",
    reset: "Reset",
    note: "Overrides last until the extension is disabled or the page reloads.",
    bg: "Background",
    text: "Text",
    muted: "Muted",
    accent: "Accent",
    danger: "Danger",
    link: "Link",
    border: "Border",
    surface: "Surface",
  });
  coddy.i18n.registerMessages("ru", {
    title: "Лаборатория тем",
    reset: "Сбросить",
    note: "Изменения цветов действуют, пока расширение не отключат или страницу не перезагрузят.",
    bg: "Фон",
    text: "Текст",
    muted: "Приглушённый цвет",
    accent: "Акцент",
    danger: "Опасность",
    link: "Ссылка",
    border: "Граница",
    surface: "Поверхность",
  });
  const names = ["bg", "text", "muted", "accent", "danger", "link", "border", "surface"];
  const channels = (text, max) => {
    const read = (part) =>
      part.endsWith("%")
        ? (parseFloat(part) / 100) * 255
        : (parseFloat(part) / max) * 255;
    const [r, g, b] = text.split(/[\s,/]+/).filter(Boolean).map(read);
    return { r, g, b };
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
  // The overrides are seeded from the theme; a slider keeps the token's hue and
  // saturation and moves only its lightness.
  const colors = {};
  const tones = {};
  const levels = {};
  let handle = null;
  const seed = () => {
    const resolved = coddy.theme.get().colors;
    for (const name of names) {
      colors[name] = resolved[name];
      const hsl = toHsl(parse(resolved[name]));
      tones[name] = { h: hsl.h, s: hsl.s };
      levels[name] = hsl.l;
    }
  };
  seed();
  const panel = coddy.ui.registerPanel({
    id: "theme-lab",
    title: coddy.i18n.t("title"),
    placement: "slide-over",
    mount(element) {
      const paint = () => {
        element.replaceChildren();
        const box = document.createElement("section");
        box.className = "ext-theme-lab";
        for (const name of names) {
          const row = document.createElement("div");
          row.className = "ext-theme-lab-row";
          const label = document.createElement("label");
          label.textContent = coddy.i18n.t(name);
          const slider = document.createElement("input");
          slider.type = "range";
          slider.min = "0";
          slider.max = "100";
          slider.value = String(levels[name]);
          const swatch = document.createElement("span");
          swatch.className = "ext-theme-lab-swatch";
          swatch.style.backgroundColor = colors[name];
          slider.addEventListener("input", () => {
            const level = Number(slider.value);
            levels[name] = level;
            const tone = tones[name];
            const value = `hsl(${tone.h}, ${tone.s}%, ${level}%)`;
            colors[name] = value;
            swatch.style.backgroundColor = value;
            if (handle) handle.update({ ...colors });
            else handle = coddy.theme.override({ ...colors });
          }, { signal: coddy.signal });
          row.append(label, slider, swatch);
          box.append(row);
        }
        const reset = document.createElement("button");
        reset.className = "settings-btn";
        reset.textContent = coddy.i18n.t("reset");
        reset.addEventListener("click", () => {
          if (handle) {
            handle.remove();
            handle = null;
          }
          seed();
          paint();
        }, { signal: coddy.signal });
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
  };
}
