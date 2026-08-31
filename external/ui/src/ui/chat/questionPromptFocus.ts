/** Move focus back to the composer after answering a gate question. */
export function questionPromptFocusComposer(): void {
  window.requestAnimationFrame(() => {
    const el =
      document.querySelector<HTMLElement>('[data-slot="composer"] textarea') ??
      document.querySelector<HTMLElement>('[data-slot="composer"]');

    try {
      el?.focus?.({ preventScroll: true });
    } catch {
      try {
        el?.focus?.();
      } catch {
        // ignore DOM focus failures
      }
    }
    try {
      el?.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      // ignore scroll failures
    }
  });
}
