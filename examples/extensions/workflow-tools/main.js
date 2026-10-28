export function activate(coddy) {
  coddy.settings.registerField({
    id: "badge",
    section: "agent",
    title: "MCP card badge",
    description: "The heading shown on the custom demo MCP card.",
    type: "string",
    default: "CUSTOM MCP",
  });
  coddy.settings.registerField({
    id: "compact",
    section: "mcp_servers",
    title: "Compact custom cards",
    type: "boolean",
    default: false,
  });
  const counts = { tabChanged: 0, stepEnded: 0, turnEnded: 0 };
  const recent = [];
  const readers = new Set();
  for (const type of Object.keys(counts)) {
    coddy.events.on(type, (event) => {
      counts[type]++;
      recent.unshift(`${type}: ${event.sessionId ?? event.to}`);
      recent.length = Math.min(recent.length, 8);
      readers.forEach((refresh) => refresh());
    });
  }
  coddy.ui.registerPage({
    id: "events",
    title: "Workflow events",
    mount(element) {
      const pre = document.createElement("pre");
      pre.style.cssText = "white-space:pre-wrap;overflow-wrap:anywhere";
      const button = document.createElement("button");
      button.textContent = "Read saved badge";
      button.className = "settings-btn";
      const saved = document.createElement("p");
      let disposed = false;
      button.addEventListener(
        "click",
        async () => {
          try {
            const value = await coddy.settings.get("badge");
            if (!disposed) saved.textContent = `Saved badge: ${value}`;
          } catch (error) {
            if (!disposed) saved.textContent = String(error);
          }
        },
        { signal: coddy.signal },
      );
      const refresh = () => {
        pre.textContent =
          Object.entries(counts)
            .map(([name, count]) => `${name}: ${count}`)
            .join("\n") +
          "\n\n" +
          recent.join("\n");
      };
      readers.add(refresh);
      refresh();
      element.append(button, saved, pre);
      return () => {
        disposed = true;
        readers.delete(refresh);
        element.replaceChildren();
      };
    },
  });
  coddy.ui.registerToolRenderer({
    id: "demo-card",
    server: "demo",
    tool: "status",
    mount(element, call) {
      const card = document.createElement("section");
      card.style.cssText =
        "border:1px solid var(--accent);border-radius:12px;padding:16px;background:var(--coddy-glass-panel-bg);display:grid;gap:8px";
      const heading = document.createElement("strong");
      heading.textContent = "CUSTOM MCP";
      const state = document.createElement("div");
      const result = document.createElement("pre");
      result.style.cssText =
        "white-space:pre-wrap;overflow-wrap:anywhere;margin:0";
      card.append(heading, state, result);
      element.append(card);
      let disposed = false;
      Promise.all([coddy.settings.get("badge"), coddy.settings.get("compact")])
        .then(([badge, compact]) => {
          if (disposed) return;
          heading.textContent = String(badge ?? "CUSTOM MCP");
          if (compact) card.style.padding = "8px";
        })
        .catch((error) => {
          if (!disposed) result.textContent = String(error);
        });
      const update = (next) => {
        state.textContent = `${next.server} / ${next.tool} · ${next.status}`;
        result.textContent = next.resultText || "Waiting for a result…";
      };
      update(call);
      return {
        update,
        dispose() {
          disposed = true;
          card.remove();
        },
      };
    },
  });
}
