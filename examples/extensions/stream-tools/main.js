export function activate(coddy) {
  let uppercase = true;
  let count = 0;
  let last = "No turn data received yet.";
  const views = new Set();
  const refresh = () => views.forEach((render) => render());
  const processor = coddy.stream.registerIncoming({
    id: "uppercase",
    transform(data, context) {
      count += 1;
      last = JSON.stringify({ ...context, data }, null, 2);
      refresh();
      if (context.event || !uppercase) return;
      const payload = JSON.parse(data);
      for (const choice of payload.choices ?? []) {
        if (typeof choice.delta?.content === "string") {
          choice.delta.content = choice.delta.content.toUpperCase();
        }
      }
      return JSON.stringify(payload);
    },
  });
  const page = coddy.ui.registerPage({
    id: "stream",
    title: "Stream processor",
    mount(element) {
      const description = document.createElement("p");
      description.textContent = "This example changes incoming text in this browser. The server keeps the original history.";
      const toggle = document.createElement("button");
      toggle.className = "settings-btn";
      const status = document.createElement("p");
      const preview = document.createElement("pre");
      preview.style.cssText = "white-space:pre-wrap;overflow-wrap:anywhere;max-height:50vh;overflow:auto";
      const render = () => {
        toggle.textContent = `Uppercase streamed text: ${uppercase ? "on" : "off"}`;
        status.textContent = `Frames processed: ${count}`;
        preview.textContent = last;
      };
      toggle.addEventListener("click", () => { uppercase = !uppercase; refresh(); }, { signal: coddy.signal });
      views.add(render);
      element.append(description, toggle, status, preview);
      render();
      return () => { views.delete(render); element.replaceChildren(); };
    },
  });
  return () => { processor.remove(); page.remove(); };
}
