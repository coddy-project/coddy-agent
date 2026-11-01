// «ОК, кодди» - a mini-chat drawer with a rubber duck, after the
// coddy-duck-prototype. The duck runs its tricks, other extensions add
// tricks through the panel's `tricks` hook, and the action buttons name
// what the extension API cannot reach yet.
const TRICKS = [
  "wave",
  "spin",
  "flip",
  "dance",
  "moonwalk",
  "turbo",
  "tongue",
  "heart",
  "magic",
  "confetti",
  "cannon",
  "sleep",
];

export function activate(coddy) {
  coddy.i18n.registerMessages("en", {
    open: "OK, coddy",
    title: "OK, coddy",
    placeholder: "Message Coddy…",
    send: "Send",
    attach: "Attach a file",
    ticket: "Send a ticket",
    ticketMade: "Ticket created: ",
    tricks: "Tricks",
    next: "Next trick",
    auto: "Auto",
    pause: "Pause",
    resume: "Resume",
    reply: "Got it: ",
    actions: "Actions",
    toFeed: "Send to the feed",
    sessions: "Session list",
    mode: "Switch mode",
    needsApi: "needs a host API this phase does not expose",
    tool: "tool: ",
    live: "live",
    modeNow: "mode: ",
    noSession: "Open a session and this mini-chat talks to it.",
    footer: "Mini-chat and duck from the OK, coddy prototype",
  });
  coddy.i18n.registerMessages("ru", {
    open: "ОК, кодди",
    title: "ОК, кодди",
    placeholder: "Сообщение для Coddy…",
    send: "Отправить",
    attach: "Прикрепить файл",
    ticket: "Отправить тикет",
    ticketMade: "Тикет создан: ",
    tricks: "Трюки",
    next: "Следующий трюк",
    auto: "Автопоказ",
    pause: "Пауза",
    resume: "Продолжить",
    reply: "Принято: ",
    actions: "Действия",
    toFeed: "Отправить в ленту",
    sessions: "Список сессий",
    mode: "Сменить режим",
    needsApi: "нужен API хоста, который эта фаза не открывает",
    tool: "инструмент: ",
    live: "вживую",
    modeNow: "режим: ",
    noSession: "Откройте сессию - и мини-чат заговорит с ней.",
    footer: "Мини-чат и уточка из прототипа «ОК, кодди»",
  });

  const state = {
    trick: 0,
    auto: !coddy.theme.getEffects().reducedMotion,
    timer: 0,
    extraTricks: [],
  };
  let panel = null;
  let item = null;
  let anchorButton = null;
  let anchorHandle = null;

  const duckSvg = () => {
    const wrap = document.createElement("div");
    wrap.className = "ok-duck";
    wrap.tabIndex = 0;
    wrap.setAttribute("role", "button");
    wrap.setAttribute("aria-label", coddy.i18n.t("next"));
    wrap.innerHTML =
      '<svg viewBox="0 0 120 110" width="120" height="110" aria-hidden="true">' +
      '<defs><linearGradient id="ok-body" x1="0" y1="0" x2="0" y2="1">' +
      '<stop offset="0" stop-color="#ffd76b"/><stop offset="1" stop-color="#f0a92e"/></linearGradient></defs>' +
      '<ellipse cx="60" cy="78" rx="40" ry="26" fill="url(#ok-body)"/>' +
      '<circle cx="82" cy="44" r="22" fill="url(#ok-body)"/>' +
      '<path d="M100 42 q16 2 16 8 q-10 4 -18 1 z" fill="#f07818"/>' +
      '<circle cx="88" cy="38" r="3.4" fill="#241d12"/>' +
      '<circle cx="89.2" cy="36.8" r="1.1" fill="#fff"/>' +
      '<path d="M28 66 q-14 -10 -20 -2 q6 10 20 8 z" fill="#e89b1d"/>' +
      '<ellipse cx="52" cy="98" rx="12" ry="4" fill="#f07818"/>' +
      "</svg>";
    return wrap;
  };

  const applyTrick = (duck, name) => {
    duck.classList.remove(...TRICKS.map((t) => "trick-" + t));
    void duck.offsetWidth;
    duck.classList.add("trick-" + name);
    duck.dataset.trick = name;
  };

  const renderPanel = (element) => {
    element.replaceChildren();
    const box = document.createElement("section");
    box.className = "ok-coddy";

    const log = document.createElement("div");
    log.className = "ok-log";
    const line = (who, text) => {
      const row = document.createElement("div");
      row.className = "ok-line ok-line-" + who;
      row.textContent = text;
      log.append(row);
      log.scrollTop = log.scrollHeight;
    };
    line("note", coddy.i18n.t("footer"));
    // A session on the address turns the mini-chat live: its turn streams
    // into the log and the input sends into the conversation.
    const sessionId = coddy.sessions.current();
    if (!sessionId) line("note", coddy.i18n.t("noSession"));
    let live = null;
    let liveLine = null;
    if (sessionId) {
      live = coddy.sessions.onMessage(sessionId, (event) => {
        if (event.type === "text") {
          if (!liveLine) {
            liveLine = document.createElement("div");
            liveLine.className = "ok-line ok-line-duck";
            log.append(liveLine);
          }
          liveLine.textContent += event.delta;
          log.scrollTop = log.scrollHeight;
        } else if (event.type === "tool") {
          liveLine = null;
          line("note", coddy.i18n.t("tool") + event.name);
        } else if (event.type === "done") {
          liveLine = null;
        }
      });
    }

    const duck = duckSvg();
    applyTrick(duck, "idle");
    const runTrick = (name) => {
      applyTrick(duck, name || nextTrick());
    };
    duck.addEventListener("click", () => runTrick(), {
      signal: coddy.signal,
    });
    duck.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        runTrick();
      }
    }, { signal: coddy.signal });

    const tray = document.createElement("div");
    tray.className = "ok-tray";
    const trayHead = document.createElement("button");
    trayHead.className = "settings-btn";
    trayHead.textContent = coddy.i18n.t("tricks");
    const trayBody = document.createElement("div");
    trayBody.className = "ok-tray-body";
    trayBody.hidden = true;
    trayHead.addEventListener("click", () => {
      trayBody.hidden = !trayBody.hidden;
    }, { signal: coddy.signal });
    const autoBtn = document.createElement("button");
    autoBtn.className = "settings-btn";
    const syncAuto = () => {
      autoBtn.textContent = state.auto
        ? coddy.i18n.t("pause")
        : coddy.i18n.t("resume");
    };
    syncAuto();
    autoBtn.addEventListener("click", () => {
      state.auto = !state.auto;
      syncAuto();
    }, { signal: coddy.signal });
    for (const name of [...TRICKS, ...state.extraTricks]) {
      const b = document.createElement("button");
      b.className = "settings-btn ok-trick-btn";
      b.textContent = name;
      b.addEventListener("click", () => {
        state.auto = false;
        syncAuto();
        runTrick(name);
      }, { signal: coddy.signal });
      trayBody.append(b);
    }
    // The tricks hook: other extensions add their own trick buttons.
    const trickSlot = document.createElement("div");
    trickSlot.className = "ok-tray-hooks";
    trayBody.append(trickSlot);
    const slot = coddy.hooks.slot("tricks", trickSlot);
    tray.append(trayHead, trayBody, autoBtn);

    const ticketRow = document.createElement("label");
    ticketRow.className = "ok-ticket";
    const ticket = document.createElement("input");
    ticket.type = "checkbox";
    ticketRow.append(ticket, document.createTextNode(coddy.i18n.t("ticket")));

    const attachRow = document.createElement("div");
    attachRow.className = "ok-attach-row";
    const attachBtn = document.createElement("button");
    attachBtn.className = "settings-btn";
    attachBtn.textContent = coddy.i18n.t("attach");
    const fileInput = document.createElement("input");
    fileInput.type = "file";
    fileInput.hidden = true;
    const chip = document.createElement("span");
    chip.className = "ok-chip";
    attachBtn.addEventListener("click", () => fileInput.click(), {
      signal: coddy.signal,
    });
    fileInput.addEventListener("change", () => {
      const file = fileInput.files && fileInput.files[0];
      chip.textContent = file ? file.name : "";
    }, { signal: coddy.signal });
    attachRow.append(attachBtn, chip);

    const input = document.createElement("input");
    input.type = "text";
    input.className = "ok-input";
    input.placeholder = coddy.i18n.t("placeholder");
    const send = document.createElement("button");
    send.className = "settings-btn";
    send.textContent = coddy.i18n.t("send");
    const sendLine = () => {
      const text = input.value.trim();
      if (!text) return;
      input.value = "";
      line("me", text);
      if (ticket.checked) {
        ticket.checked = false;
        line("duck", coddy.i18n.t("ticketMade") + text);
        return;
      }
      if (sessionId) {
        coddy.sessions
          .send({ sessionId: sessionId, text: text })
          .then((result) => {
            if (!result.accepted) line("note", result.error);
          })
          .catch((error) => line("note", String(error)));
        return;
      }
      window.setTimeout(() => line("duck", coddy.i18n.t("reply") + text), 250);
    };
    send.addEventListener("click", sendLine, { signal: coddy.signal });
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") sendLine();
    }, { signal: coddy.signal });

    const actions = document.createElement("div");
    actions.className = "ok-actions";
    for (const label of ["toFeed", "sessions", "mode"]) {
      const b = document.createElement("button");
      b.className = "settings-btn";
      b.disabled = true;
      b.textContent = coddy.i18n.t(label);
      b.title = coddy.i18n.t("needsApi");
      if (label === "mode") {
        // The composer API exists now: the button cycles the session mode.
        b.disabled = false;
        b.title = coddy.i18n.t("live");
        b.addEventListener(
          "click",
          () => {
            const order = ["agent", "plan", "ask"];
            const current = coddy.composer.getMode();
            const next =
              order[(order.indexOf(current) + 1) % order.length] || "agent";
            coddy.composer.setMode(next);
            line("note", coddy.i18n.t("modeNow") + next);
          },
          { signal: coddy.signal },
        );
      }
      if (label === "toFeed" && sessionId) {
        // The feed action is the sessions API now; the other two still
        // wait for coddy.nav and composer controls.
        b.disabled = false;
        b.title = coddy.i18n.t("live");
        b.addEventListener(
          "click",
          () => {
            const text = input.value.trim();
            if (!text) return;
            input.value = "";
            line("me", text);
            coddy.sessions
              .send({ sessionId: sessionId, text: text })
              .then((result) => {
                if (!result.accepted) line("note", result.error);
              })
              .catch((error) => line("note", String(error)));
          },
          { signal: coddy.signal },
        );
      }
      actions.append(b);
    }

    const footerSlot = document.createElement("div");
    footerSlot.className = "ok-footer-hooks";
    const footer = coddy.hooks.slot("footer", footerSlot);

    box.append(
      log,
      duck,
      tray,
      ticketRow,
      attachRow,
      input,
      send,
      actions,
      footerSlot,
    );
    element.append(box);
    // Focused without scrolling the drawer away from its own top.
    try {
      input.focus({ preventScroll: true });
    } catch (e) {
      input.focus();
    }
    return () => {
      if (live) live.remove();
      slot.remove();
      footer.remove();
      stopAuto();
    };
  };

  const nextTrick = () => {
    const all = [...TRICKS, ...state.extraTricks];
    state.trick = (state.trick + 1) % all.length;
    return all[state.trick];
  };
  const stopAuto = () => {
    if (state.timer) {
      window.clearInterval(state.timer);
      state.timer = 0;
    }
  };
  const startAuto = () => {
    stopAuto();
    state.timer = window.setInterval(() => {
      if (!state.auto || document.hidden || !panel) return;
      const duck = document.querySelector(".ok-duck");
      if (duck) applyTrick(duck, nextTrick());
    }, 2600);
  };
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) stopAuto();
    else if (panel) startAuto();
  }, { signal: coddy.signal });

  const open = () => {
    if (panel) return;
    panel = coddy.ui.registerPanel({
      id: "ok-coddy",
      title: coddy.i18n.t("title"),
      placement: "drawer",
      // The panel's own description extends the API: other extensions may
      // fill these hooks through coddy.hooks.register.
      hooks: ["tricks", "footer"],
      mount(element) {
        const cleanup = renderPanel(element);
        startAuto();
        return () => {
          cleanup();
          stopAuto();
          panel = null;
        };
      },
    });
    coddy.ui.openPanel("ok-coddy");
  };

  item = coddy.ui.registerMenuItem({
    id: "open-ok-coddy",
    title: coddy.i18n.t("open"),
    onSelect: open,
  });
  const button = document.createElement("button");
  button.type = "button";
  button.className = "settings-btn ok-open";
  button.textContent = coddy.i18n.t("open");
  button.addEventListener("click", open, { signal: coddy.signal });
  anchorButton = button;
  // onAnchor fires at once when the anchor is already on screen, and again
  // whenever its surface comes and goes.
  anchorHandle = coddy.ui.onAnchor("composer.actions", (box) => {
    if (box) box.append(button);
  });

  return () => {
    if (anchorHandle) anchorHandle.remove();
    if (anchorButton) anchorButton.remove();
    stopAuto();
    if (item) item.remove();
    if (panel) panel.remove();
  };
}
