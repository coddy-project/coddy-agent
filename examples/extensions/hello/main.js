export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "counter",
    title: "Hello extension",
    mount(element, api) {
      let mounted = true;
      api.assets.text("panel.html").then((html) => {
        if (!mounted || api.signal.aborted) return;
        element.innerHTML = html;
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
      return () => { mounted = false; element.replaceChildren(); };
    },
  });
  const action = coddy.ui.registerMenuItem({
    id: "open-counter",
    title: "Open counter",
    onSelect: () => coddy.ui.openPage("counter"),
  });
  return () => { action.remove(); page.remove(); };
}
