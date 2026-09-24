import { snapshotTouchOnly } from "../shellBreakpoint";

/**
 * Whether the app may put the caret in the composer by itself: on opening the
 * start screen, on switching to a conversation, on closing History. On a phone
 * or a tablet (a touch-only device, `touchOnlyMediaQuery`) a focused field
 * opens the on-screen keyboard over half of the screen, so there the keyboard
 * waits for the reader to tap the field. Like Enter (composerEnter.ts), this
 * follows the input device, not the width: a narrow desktop window has a
 * keyboard and keeps the focus.
 */
export function composerAutoFocusAllowed(): boolean {
  return !snapshotTouchOnly();
}
