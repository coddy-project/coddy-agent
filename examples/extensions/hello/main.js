export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "counter",
    title: "Hello extension",
    mount(element, api) {
      let mounted = true;
      const themeBox = document.createElement("div");
      themeBox.className = "ext-hello-theme-box";
      const paint = (theme) => {
        themeBox.replaceChildren();
        const label = document.createElement("p");
        label.className = "ext-hello-theme";
        label.textContent = `Theme: ${theme.id}`;
        const row = document.createElement("div");
        row.className = "ext-hello-swatches";
        for (const [name, color] of Object.entries(theme.colors)) {
          const swatch = document.createElement("span");
          swatch.className = "ext-hello-swatch";
          swatch.style.backgroundColor = color;
          swatch.title = `${name}: ${color}`;
          row.append(swatch);
        }
        themeBox.append(label, row);
      };
      const watched = api.theme.onChange(paint);
      paint(api.theme.get());
      api.assets.text("panel.html").then((html) => {
        if (!mounted || api.signal.aborted) return;
        element.innerHTML = html;
        element.querySelector(".ext-hello").append(themeBox);
        let count = 0;
        const button = element.querySelector("button");
        button.addEventListener("click", () => {
          count += 1;
          button.textContent = `Clicked ${count} times`;
          action.update({ title: `Counter: ${count}` });
        }, { signal: api.signal });
      }).catch((error) => {
        if (mounted && !api.signal.aborted) element.textContent = String(error);
      });
      return () => {
        mounted = false;
        watched.remove();
        element.replaceChildren();
      };
    },
  });
  const action = coddy.ui.registerMenuItem({
    id: "open-counter",
    title: "Open counter",
    onSelect: () => coddy.ui.openPage("counter"),
  });
  return () => { action.remove(); page.remove(); };
}
