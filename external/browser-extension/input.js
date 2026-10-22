// Chrome provides trusted input through CDP. Firefox falls back to page APIs
// unless a native input host is explicitly enabled by the user.
export async function chromeInput(api, tabId, action, params) {
  const target = { tabId };
  const send = (method, data) => api.debugger.sendCommand(target, method, data);
  // DevTools keeps its own debugger attachment on the tab, and Chrome reports
  // that lowercase ("Another debugger is already attached to the tab with
  // id: N."). Attach is then skipped, and the detach below has to be skipped
  // too — it would cut DevTools off.
  let attached = false;
  try {
    await api.debugger.attach(target, "1.3");
    attached = true;
  } catch (e) {
    if (!/already attached/i.test(String(e))) throw e;
  }
  try {
    if (action === "screenshot") {
      const { data } = await send("Page.captureScreenshot", { format: "png" });
      return { data_url: `data:image/png;base64,${data}` };
    }
    if (action === "click") {
      await send("Input.dispatchMouseEvent", {
        type: "mousePressed",
        x: params.x,
        y: params.y,
        button: "left",
        clickCount: 1,
      });
      await send("Input.dispatchMouseEvent", {
        type: "mouseReleased",
        x: params.x,
        y: params.y,
        button: "left",
        clickCount: 1,
      });
    } else if (action === "type") {
      if (params.clear) {
        await send("Input.dispatchKeyEvent", {
          type: "rawKeyDown",
          key: "a",
          code: "KeyA",
          modifiers: navigator.platform.includes("Mac") ? 4 : 2,
          windowsVirtualKeyCode: 65,
        });
        await send("Input.dispatchKeyEvent", {
          type: "keyUp",
          key: "a",
          code: "KeyA",
          windowsVirtualKeyCode: 65,
        });
      }
      await send("Input.insertText", { text: params.text });
    } else if (action === "press") {
      const keys = {
        Enter: [13, "Enter", "\r"],
        Tab: [9, "Tab"],
        Backspace: [8, "Backspace"],
        Delete: [46, "Delete"],
        Escape: [27, "Escape"],
        ArrowLeft: [37, "ArrowLeft"],
        ArrowUp: [38, "ArrowUp"],
        ArrowRight: [39, "ArrowRight"],
        ArrowDown: [40, "ArrowDown"],
        Home: [36, "Home"],
        End: [35, "End"],
        Space: [32, "Space", " "],
      };
      const parts = params.key.split("+"),
        name = parts.pop();
      let modifiers = 0;
      for (const m of parts) {
        const bit = { Ctrl: 2, Control: 2, Alt: 1, Shift: 8, Meta: 4, Cmd: 4 }[
          m
        ];
        if (!bit) throw new Error("Unsupported key modifier");
        modifiers |= bit;
      }
      const k =
        keys[name] ||
        (/^[a-z0-9]$/i.test(name)
          ? [
              name.toUpperCase().charCodeAt(0),
              /^\d$/.test(name) ? `Digit${name}` : `Key${name.toUpperCase()}`,
              modifiers & 7 ? undefined : name,
            ]
          : null);
      if (!k) throw new Error("Unsupported key");
      await send("Input.dispatchKeyEvent", {
        type: "keyDown",
        key: name,
        code: k[1],
        windowsVirtualKeyCode: k[0],
        text: k[2],
        modifiers,
      });
      await send("Input.dispatchKeyEvent", {
        type: "keyUp",
        key: name,
        code: k[1],
        windowsVirtualKeyCode: k[0],
        modifiers,
      });
    } else if (action === "scroll") {
      await send("Input.dispatchMouseEvent", {
        type: "mouseWheel",
        x: params.x ?? 100,
        y: params.y ?? 100,
        deltaX: params.dx || 0,
        deltaY: params.dy || 0,
      });
    } else if (action === "drag") {
      const check = () => {
        if (params.cancelled?.()) throw new Error("Stopped");
      };
      check();
      await send("Input.dispatchMouseEvent", {
        type: "mousePressed",
        x: params.x,
        y: params.y,
        button: "left",
        clickCount: 1,
      });
      try {
        for (let step = 1; step <= 16; step++) {
          check();
          await send("Input.dispatchMouseEvent", {
            type: "mouseMoved",
            x: params.x + ((params.to_x - params.x) * step) / 16,
            y: params.y + ((params.to_y - params.y) * step) / 16,
            button: "left",
            buttons: 1,
          });
        }
      } finally {
        await send("Input.dispatchMouseEvent", {
          type: "mouseReleased",
          x: params.to_x,
          y: params.to_y,
          button: "left",
          clickCount: 1,
        });
      }
    } else throw new Error("Unsupported native action");
    return { ok: true, input: "native" };
  } finally {
    if (attached) {
      try {
        await api.debugger.detach(target);
      } catch {}
    }
  }
}

export function nativeInput(api, params, signal) {
  return new Promise((resolve, reject) => {
    const port = api.runtime.connectNative("dev.coddy.browser");
    let finished = false;
    const finish = (err, value) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", cancel);
      port.disconnect();
      err ? reject(err) : resolve(value);
    };
    const cancel = () => finish(new Error("Native input stopped"));
    const timer = setTimeout(
      () => finish(new Error("Native input timed out")),
      30000,
    );
    port.onMessage.addListener((reply) =>
      finish(reply.error ? new Error(reply.error) : null, reply),
    );
    port.onDisconnect.addListener(() => {
      const error = api.runtime.lastError;
      finish(new Error(error?.message || "Native input host disconnected"));
    });
    if (signal?.aborted) {
      cancel();
      return;
    }
    signal?.addEventListener("abort", cancel, { once: true });
    port.postMessage(params);
  });
}
