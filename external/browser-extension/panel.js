const api = globalThis.browser || chrome;
const $ = (id) => document.getElementById(id);
let session = "",
  state,
  initialized = false,
  busy = false,
  rendered = "";
async function request(msg) {
  const r = await api.runtime.sendMessage(msg);
  if (r.error) throw new Error(r.error);
  return r.value;
}
function error(e) {
  $("error").hidden = !e;
  $("error").textContent = e?.message || String(e || "");
}
function button(label, action) {
  const b = document.createElement("button");
  b.textContent = label;
  b.onclick = () => action().catch(error);
  return b;
}
async function refresh() {
  try {
    state = await request({ type: "state" });
    $("status").textContent = state.status;
    if (!initialized) {
      $("address").value = state.config.address || "http://localhost:12348";
      $("name").value = state.config.name;
      $("native-input").checked = state.config.nativeInput;
      $("connection").open = !state.config.enabled;
      initialized = true;
    }
    $("input-mode").textContent = state.native
      ? "Native browser input available."
      : "Firefox: page input is available. Some editors require the optional native input host.";
    const next = JSON.stringify([
      state.tabs,
      state.shared,
      state.active,
      state.pending,
      state.allowed,
    ]);
    // Keep controls and keyboard focus stable between unchanged poll results.
    if (rendered === next) return;
    rendered = next;
    const tabs = $("tabs");
    tabs.replaceChildren();
    for (const tab of state.tabs) {
      const label = document.createElement("label");
      label.className = "tab";
      const check = document.createElement("input");
      check.type = "checkbox";
      check.checked = state.shared.includes(tab.id);
      check.onchange = async () => {
        try {
          const ids = new Set(state.shared);
          check.checked ? ids.add(tab.id) : ids.delete(tab.id);
          await request({ type: "share", ids: [...ids] });
          await refresh();
        } catch (e) {
          error(e);
        }
      };
      const text = document.createElement("span");
      text.textContent = tab.title || tab.url;
      const small = document.createElement("small");
      small.textContent =
        new URL(tab.url).host +
        (state.active.some((a) => a.tab === tab.id) ? " · Working" : "");
      text.append(small);
      label.append(check, text);
      tabs.append(label);
    }
    if (!state.tabs.length) tabs.textContent = "Open a website to share a tab.";
    $("permissions").replaceChildren();
    for (const p of state.pending) {
      const box = document.createElement("div");
      box.className = "permission";
      const title = document.createElement("strong");
      title.textContent = "Allow website access?";
      const site = document.createElement("p");
      site.textContent = p.origin;
      box.append(title, site);
      for (const [label, choice] of [
        ["Allow now", "once"],
        ["Always allow site", "always"],
        ["Decline", "deny"],
      ])
        box.append(
          button(label, async () => {
            await request({ type: "permission", id: p.id, choice });
            await refresh();
          }),
        );
      $("permissions").append(box);
    }
    $("allowed").replaceChildren();
    for (const origin of state.allowed) {
      const row = document.createElement("div");
      row.className = "website";
      const text = document.createElement("span");
      text.textContent = origin;
      row.append(
        text,
        button("Revoke", async () => {
          await request({ type: "revoke", origin });
          await refresh();
        }),
      );
      $("allowed").append(row);
    }
  } catch (e) {
    error(e);
  }
}
$("connect-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await request({
      type: "save",
      address: $("address").value,
      key: $("key").value,
      name: $("name").value,
      nativeInput: $("native-input").checked,
    });
    $("key").value = "";
    $("connection").open = false;
    error(null);
    await refresh();
    await loadChats();
  } catch (err) {
    error(err);
  }
};
$("stop").onclick = () => request({ type: "stop" }).then(refresh).catch(error);
$("expand").onclick = async () => {
  try {
    if (api.sidePanel) {
      const window = await api.windows.getCurrent();
      await api.sidePanel.open({ windowId: window.id });
    } else if (api.sidebarAction) await api.sidebarAction.open();
    else
      await api.windows.create({
        url: api.runtime.getURL("panel.html"),
        type: "popup",
        width: 420,
        height: 800,
      });
  } catch (e) {
    error(e);
  }
};
$("disconnect").onclick = () =>
  request({ type: "disconnect" }).then(refresh).catch(error);
async function chat(path, method, body) {
  return request({ type: "chat", path, method, body, session });
}
function showMessage(role, text) {
  const node = document.createElement("div");
  node.className = `message ${role}`;
  node.textContent = text;
  $("messages").append(node);
  node.scrollIntoView({ block: "nearest" });
  return node;
}
async function loadChats() {
  const { data } = await chat("/coddy/sessions");
  const rows = data.sessions || data.data || [];
  $("sessions").replaceChildren(new Option("New chat", ""));
  for (const s of rows) {
    const id = s.id || s.sessionId || s.session_id;
    if (id) $("sessions").add(new Option(s.title || id, id));
  }
  // The list holds only the conversations this key started. A stored one that
  // is no longer in it (the server restarted and forgot which were ours) would
  // be refused on the next send, so drop it and start a new chat instead.
  if (session && ![...$("sessions").options].some((o) => o.value === session)) {
    session = "";
    await api.storage.local.set({ panelSession: session });
  }
  $("sessions").value = session;
  const models = await chat("/v1/models");
  $("model").replaceChildren(new Option("Agent", "agent"));
  for (const m of models.data.data || [])
    if (m.owned_by !== "coddy") $("model").add(new Option(m.id, m.id));
}
$("refresh").onclick = () => loadChats().catch(error);
$("sessions").onchange = async () => {
  session = $("sessions").value;
  await api.storage.local.set({ panelSession: session });
  $("messages").replaceChildren();
  if (session) {
    try {
      const { data } = await chat(
        `/coddy/sessions/${encodeURIComponent(session)}/messages`,
      );
      for (const m of data.messages || []) {
        if (m.content && typeof m.content === "string")
          showMessage(m.role, m.content);
      }
    } catch (e) {
      error(e);
    }
  }
};
$("chat-form").onsubmit = async (e) => {
  e.preventDefault();
  if (busy) return;
  busy = true;
  $("send").disabled = true;
  const prompt = $("prompt").value;
  showMessage("user", prompt);
  $("prompt").value = "";
  // A new chat deliberately sends no session id. The server mints one and
  // reports it back (the "session" event below), which is what keeps the
  // extension key to the conversations this panel started rather than to
  // every session on the server.
  const answer = showMessage("assistant", "Working…");
  let text = "";
  const port = api.runtime.connect({ name: "coddy-chat" });
  const finish = () => {
    busy = false;
    $("send").disabled = false;
    port.disconnect();
  };
  port.onDisconnect.addListener(() => {
    busy = false;
    $("send").disabled = false;
  });
  port.onMessage.addListener((msg) => {
    if (msg.event === "finished") {
      if (!text) answer.textContent = "Turn finished.";
      finish();
      return;
    }
    if (msg.event === "failed") {
      error(msg.error);
      finish();
      return;
    }
    const data = msg.data;
    // Keep the conversation the server minted for this turn, so a follow-up,
    // cancel and the chat list all name the same one. Permission and question
    // frames carry their own sessionId as a fallback.
    if (data?.sessionId && (msg.event === "session" || !session)) {
      session = data.sessionId;
      void api.storage.local.set({ panelSession: session });
    }
    if (msg.event === "permission") {
      const box = document.createElement("div");
      box.className = "permission";
      const description = document.createElement("p");
      description.textContent = data.toolCall?.title || "Allow this tool?";
      box.append(description);
      const args = data.toolCall?.content?.[0]?.content?.text;
      if (args) {
        const pre = document.createElement("pre");
        pre.textContent = args.slice(0, 2000);
        box.append(pre);
      }
      for (const option of data.options || [])
        box.append(
          button(option.name || option.label || option.optionId, async () => {
            await chat(
              `/coddy/sessions/${encodeURIComponent(data.sessionId || session)}/permission`,
              "POST",
              {
                toolCallId: data.toolCall.toolCallId,
                optionId: option.optionId,
              },
            );
            box.remove();
          }),
        );
      $("chat-permissions").append(box);
    } else if (msg.event === "question") {
      const form = document.createElement("form");
      form.className = "permission";
      const fields = [];
      for (const q of data.questions || []) {
        const label = document.createElement("label");
        label.textContent = q.question;
        const input = document.createElement("input");
        input.placeholder = (q.options || []).map((o) => o.label).join(" / ");
        label.append(input);
        form.append(label);
        fields.push(input);
      }
      const submit = document.createElement("button");
      submit.type = "submit";
      submit.textContent = "Answer";
      form.append(submit);
      form.onsubmit = async (event) => {
        event.preventDefault();
        try {
          await chat(
            `/coddy/sessions/${encodeURIComponent(data.sessionId || session)}/question`,
            "POST",
            {
              requestId: data.requestId,
              answers: fields.map((f) => [f.value]),
            },
          );
          form.remove();
        } catch (e) {
          error(e);
        }
      };
      $("chat-permissions").append(form);
    } else {
      const delta =
        data.choices?.[0]?.delta?.content ||
        (msg.event === "response.output_text.delta" ? data.delta : "");
      if (delta) {
        text += delta;
        answer.textContent = text;
      }
      if (data.error) error(data.error.message || data.error);
    }
  });
  port.postMessage({
    session,
    input: prompt,
    model: $("model").value === "agent" ? undefined : $("model").value,
  });
};
$("cancel").onclick = () =>
  session
    ? chat(
        `/coddy/sessions/${encodeURIComponent(session)}/cancel`,
        "POST",
        {},
      ).catch(error)
    : undefined;
session = (await api.storage.local.get("panelSession")).panelSession || "";
await refresh();
if (state?.config.enabled) await loadChats().catch(error);
setInterval(refresh, 1500);
