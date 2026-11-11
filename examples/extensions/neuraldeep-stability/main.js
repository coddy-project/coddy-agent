const UPTIME_URL = "https://status.neuraldeep.ru/api/v2/uptime.json";
const STATUS_PAGE = "https://status.neuraldeep.ru/";

function node(tag, className, text) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (text !== undefined) el.textContent = text;
  return el;
}

function percent(value) {
  return typeof value === "number" && Number.isFinite(value)
    ? `${value.toFixed(2)}%`
    : "n/a";
}

function statusLabel(status) {
  return status === "operational" ? "Operational" : "Attention required";
}

function historyTitle(day) {
  const details = [`${day.date}: ${percent(day.uptime_pct)}`];
  if (day.down_checks) details.push(`${day.down_checks} failed checks`);
  if (Array.isArray(day.windows)) details.push(...day.windows);
  return details.join("\n");
}

/** Renders the uptime API without exposing the browser to its CORS policy:
 * coddy.net.fetch performs this request on the Coddy server. */
export function activate(coddy) {
  const block = coddy.settings.registerBlock({
    id: "stability",
    section: "providers",
    title: "Stability",
    description:
      "NeuralDeep uptime and daily history from status.neuraldeep.ru",
    match: ({ row }) => row?.type === "neuraldeep",
    mount(element, api) {
      const controller = new AbortController();
      let disposed = false;
      const active = () => {
        if (disposed || controller.signal.aborted || api.signal.aborted) {
          controller.abort();
          return false;
        }
        return true;
      };
      api.signal.addEventListener("abort", () => controller.abort(), {
        once: true,
      });

      const root = node("div", "nd-stability");
      const summary = node("div", "nd-stability-summary");
      const list = node("div", "nd-stability-list");
      const footer = node("div", "nd-stability-footer");
      root.append(summary, list, footer);
      element.append(root);

      const retry = node("button", "settings-btn nd-stability-retry", "Retry");
      retry.type = "button";
      retry.addEventListener("click", () => void load(), {
        signal: controller.signal,
      });
      const link = node("a", "nd-stability-link", "Open status page");
      link.href = STATUS_PAGE;
      link.target = "_blank";
      link.rel = "noreferrer";
      footer.append(retry, link);

      function render(data) {
        const components = Array.isArray(data.components)
          ? data.components
          : [];
        const operational = components.every(
          (component) => component.status === "operational",
        );
        summary.replaceChildren(
          node(
            "strong",
            `nd-stability-state${operational ? " is-ok" : " is-bad"}`,
            components.length && operational
              ? "All systems operational"
              : "Attention required",
          ),
          node(
            "span",
            "nd-stability-updated",
            data.page?.updated_at ? `Updated ${data.page.updated_at}` : "",
          ),
        );
        list.replaceChildren(
          ...components.map((component) => {
            const card = node("section", "nd-stability-component");
            const head = node("div", "nd-stability-component-head");
            head.append(
              node("strong", "", component.name || component.id || "Component"),
              node(
                "span",
                `nd-stability-uptime${component.status === "operational" ? " is-ok" : " is-bad"}`,
                percent(component.uptime_pct),
              ),
            );
            const days = node("div", "nd-stability-days", undefined);
            const history = Array.isArray(data.history?.[component.id])
              ? data.history[component.id].slice(-30)
              : [];
            for (const day of history) {
              const bar = node("span", "nd-stability-day");
              bar.dataset.status = String(day.status ?? 0);
              bar.title = historyTitle(day);
              bar.setAttribute("aria-label", historyTitle(day));
              days.append(bar);
            }
            if (!history.length)
              days.append(
                node("span", "nd-stability-empty", "No daily history"),
              );
            card.append(head, days);
            return card;
          }),
        );
      }

      async function load() {
        if (!active()) return;
        summary.textContent = "Loading uptime…";
        list.replaceChildren();
        retry.disabled = true;
        try {
          const response = await api.net.fetch(UPTIME_URL, {
            signal: controller.signal,
          });
          if (!response.ok)
            throw new Error(`Uptime API returned HTTP ${response.status}`);
          const data = await response.json();
          if (!active()) return;
          render(data);
        } catch (error) {
          if (!active()) return;
          summary.textContent = `Could not load uptime: ${String(error)}`;
        } finally {
          if (active()) retry.disabled = false;
        }
      }
      void load();
      return () => {
        disposed = true;
        controller.abort();
        root.remove();
      };
    },
  });
  return () => block.remove();
}
