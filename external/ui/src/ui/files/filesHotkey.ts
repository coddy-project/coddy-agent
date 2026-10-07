/**
 * The key of the Files window: Ctrl+Shift+F, Cmd+Shift+F on a Mac. Matched on
 * the key's place (`code`), not on the character it types, so the Russian
 * layout, where the key types "А", opens the window too. A held key repeats
 * nothing, and Alt keeps its own meaning.
 */
export function isFilesHotkey(ev: KeyboardEvent): boolean {
  return (
    ev.code === "KeyF" &&
    ev.shiftKey &&
    (ev.ctrlKey || ev.metaKey) &&
    !ev.altKey &&
    !ev.repeat &&
    !ev.isComposing
  );
}

/** Apple keyboards name the modifiers by their symbols. */
function isApplePlatform(): boolean {
  if (typeof navigator === "undefined") {
    return false;
  }
  return /Mac|iPhone|iPad|iPod/i.test(
    navigator.platform || navigator.userAgent || "",
  );
}

/** The key as the menu prints it beside Files. */
export function filesShortcutLabel(): string {
  return isApplePlatform() ? "⇧⌘F" : "Ctrl+Shift+F";
}
