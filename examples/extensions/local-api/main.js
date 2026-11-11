export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "request",
    title: "Local API",
    mount(element) {
      const form = document.createElement("form");
      form.style.cssText = "display:grid;gap:12px;min-width:0";
      form.innerHTML = `
        <p>Requests run on the Coddy server. Localhost refers to that server.</p>
        <label>URL <input name="url" type="url" required value="http://127.0.0.1:8080/api/status" style="width:100%"></label>
        <label>Method <select name="method"><option>GET</option><option>POST</option></select></label>
        <label>JSON body for POST <textarea name="body" rows="3" style="width:100%">{}</textarea></label>
        <div><button type="submit">Send request</button> <button type="button" name="cancel" disabled>Cancel</button></div>
        <pre role="status" style="white-space:pre-wrap;overflow-wrap:anywhere">Ready</pre>`;
      const field = (name) => form.elements.namedItem(name);
      const output = form.querySelector("pre");
      let request;
      const cancel = field("cancel");
      cancel.addEventListener("click", () => request?.abort());
      form.addEventListener("submit", async (event) => {
        event.preventDefault();
        request?.abort();
        const current = new AbortController();
        request = current;
        cancel.disabled = false;
        output.textContent = "Sending…";
        try {
          const method = field("method").value;
          const response = await coddy.net.fetch(field("url").value, {
            method,
            ...(method === "POST" ? {
              headers: { "Content-Type": "application/json" },
              body: field("body").value
            } : {}),
            signal: current.signal
          });
          const text = await response.text();
          if (request === current)
            output.textContent = `HTTP ${response.status}\n${text}`;
        } catch (error) {
          if (request === current) output.textContent = String(error);
        } finally {
          if (request === current) cancel.disabled = true;
        }
      });
      element.append(form);
      return () => { request?.abort(); form.remove(); };
    }
  });
  return () => page.remove();
}
