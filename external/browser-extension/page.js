// This function is serialized by scripting.executeScript. Keep every helper
// inside it and never accept JavaScript source from a page or agent.
export async function pageAction(action, params) {
  if (!params.expected_origin || location.origin !== params.expected_origin)
    throw new Error("Page changed before the action; inspect it again");
  const state = (globalThis.__coddyPageState ||= {
    refs: new Map(),
    nonce: crypto.randomUUID(),
  });
  const visible = (e) =>
    e.getClientRects().length && getComputedStyle(e).visibility !== "hidden";
  const element = () => {
    const e = params.ref
      ? state.refs.get(params.ref)
      : document.elementFromPoint(params.x, params.y);
    if (!e || !e.isConnected)
      throw new Error("Element no longer exists; take a new snapshot");
    return e;
  };
  if (action === "screen") {
    return {
      origin_x:
        window.mozInnerScreenX ?? screenX + (outerWidth - innerWidth) / 2,
      origin_y: window.mozInnerScreenY ?? screenY + outerHeight - innerHeight,
      scale: devicePixelRatio,
      title: document.title,
      width: innerWidth,
      height: innerHeight,
      has_focus: document.hasFocus(),
    };
  }
  if (action === "snapshot") {
    state.refs.clear();
    state.nonce = crypto.randomUUID();
    const items = [];
    function walk(root) {
      for (const e of root.querySelectorAll("*")) {
        if (e.shadowRoot) walk(e.shadowRoot);
        if (
          !e.matches(
            'a,button,input,textarea,select,[role],[contenteditable="true"],summary',
          ) ||
          !visible(e)
        )
          continue;
        if (items.length >= 500) break;
        const ref = `${state.nonce}:${items.length + 1}`;
        state.refs.set(ref, e);
        const rect = e.getBoundingClientRect();
        const password = e instanceof HTMLInputElement && e.type === "password";
        items.push({
          ref,
          tag: e.tagName.toLowerCase(),
          role: e.getAttribute("role"),
          type: e.type,
          name: (
            e.getAttribute("aria-label") ||
            e.labels?.[0]?.innerText ||
            e.innerText ||
            e.getAttribute("placeholder") ||
            ""
          ).slice(0, 250),
          value: password
            ? undefined
            : typeof e.value === "string"
              ? e.value.slice(0, 250)
              : undefined,
          x: Math.round(rect.x + rect.width / 2),
          y: Math.round(rect.y + rect.height / 2),
        });
      }
    }
    walk(document);
    return {
      title: document.title,
      url: location.href,
      text: (document.body?.innerText || "").slice(0, 20000),
      elements: items,
      viewport: { width: innerWidth, height: innerHeight },
    };
  }
  if (action === "target") {
    const e = element();
    e.scrollIntoView({ block: "center", inline: "center" });
    e.focus();
    const r = e.getBoundingClientRect();
    return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
  }
  if (action === "click") {
    const e = element();
    e.scrollIntoView({ block: "center" });
    e.focus();
    e.click();
    return { ok: true, input: "DOM" };
  }
  if (action === "type") {
    const e = params.ref ? element() : document.activeElement;
    if (!e) throw new Error("Focus an editor first");
    if (e.type === "password") throw new Error("Enter passwords manually");
    e.focus();
    if (e instanceof HTMLInputElement || e instanceof HTMLTextAreaElement) {
      const setter = Object.getOwnPropertyDescriptor(
        e instanceof HTMLInputElement
          ? HTMLInputElement.prototype
          : HTMLTextAreaElement.prototype,
        "value",
      ).set;
      const start = params.clear ? 0 : (e.selectionStart ?? e.value.length);
      const end = params.clear ? e.value.length : (e.selectionEnd ?? start);
      setter.call(
        e,
        e.value.slice(0, start) + params.text + e.value.slice(end),
      );
      e.dispatchEvent(
        new InputEvent("input", {
          bubbles: true,
          inputType: "insertText",
          data: params.text,
        }),
      );
      e.dispatchEvent(new Event("change", { bubbles: true }));
    } else if (e.isContentEditable) {
      if (params.clear) {
        const range = document.createRange();
        range.selectNodeContents(e);
        const s = getSelection();
        s.removeAllRanges();
        s.addRange(range);
      }
      if (!document.execCommand("insertText", false, params.text))
        throw new Error(
          "Editor rejected DOM insertion; native input is required",
        );
    } else {
      throw new Error(
        "This editor needs native input; DOM insertion is unavailable",
      );
    }
    return { ok: true, input: "DOM" };
  }
  if (action === "press") {
    const e = document.activeElement || document.body;
    for (const type of ["keydown", "keyup"])
      e.dispatchEvent(
        new KeyboardEvent(type, { key: params.key, bubbles: true }),
      );
    return {
      ok: true,
      input: "synthetic",
      warning:
        "Synthetic keys do not perform native browser editing; inspect the page.",
    };
  }
  if (action === "scroll") {
    scrollBy({
      left: params.dx || 0,
      top: params.dy || 0,
      behavior: "instant",
    });
    return { ok: true };
  }
  if (action === "drag") {
    const from = document.elementFromPoint(params.x, params.y),
      to = document.elementFromPoint(params.to_x, params.to_y);
    if (!from || !to)
      throw new Error("Drag endpoints are outside the viewport");
    const dataTransfer = new DataTransfer();
    from.dispatchEvent(
      new DragEvent("dragstart", { bubbles: true, dataTransfer }),
    );
    to.dispatchEvent(
      new DragEvent("dragenter", { bubbles: true, dataTransfer }),
    );
    to.dispatchEvent(
      new DragEvent("dragover", {
        bubbles: true,
        cancelable: true,
        dataTransfer,
      }),
    );
    to.dispatchEvent(
      new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer }),
    );
    from.dispatchEvent(
      new DragEvent("dragend", { bubbles: true, dataTransfer }),
    );
    return {
      ok: true,
      input: "synthetic",
      warning: "Inspect the page to confirm the drop.",
    };
  }
  if (action === "upload_file") {
    const e = element();
    if (!(e instanceof HTMLInputElement) || e.type !== "file")
      throw new Error("Choose a file input ref from snapshot");
    const bytes = Uint8Array.from(atob(params.base64), (c) => c.charCodeAt(0));
    const transfer = new DataTransfer();
    transfer.items.add(new File([bytes], params.filename));
    e.files = transfer.files;
    e.dispatchEvent(new Event("input", { bubbles: true }));
    e.dispatchEvent(new Event("change", { bubbles: true }));
    return { ok: true, filename: params.filename, bytes: bytes.length };
  }
  throw new Error("Unsupported page action");
}
