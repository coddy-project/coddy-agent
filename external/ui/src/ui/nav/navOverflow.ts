/**
 * The top bar of a phone and a tablet is a fixed row: History, Scheduler, the
 * environment the page drives and, on a relay, the swarm, then the More button
 * that holds everything else. Nothing is measured, so the row reads the same
 * however much room the window has; the desktop rail never folds and shows
 * every item it has.
 *
 * `NAV_BAR` is the row in bar order. `NAV_MENU_ORDER` is the menu under More:
 * the installation's own surfaces first (extensions, documentation, statistics,
 * then the settings), a separator, and signing out last.
 */
export type NavItemId =
  | "statistics"
  | "history"
  | "scheduler"
  | "swarm"
  | "docs"
  | "settings"
  | "extensions"
  | "environment"
  | "signOut";

export const NAV_BAR: readonly NavItemId[] = [
  "history",
  "scheduler",
  "environment",
  "swarm",
];

export const NAV_MENU_ORDER: readonly NavItemId[] = [
  "extensions",
  "docs",
  "statistics",
  "settings",
  "signOut",
];

/**
 * Splits the items the rail has (`present`) into the bar and the menu under
 * More. An item outside both lists, such as the anchor slot, stays wherever
 * the caller renders it.
 */
export function splitNavItems(present: readonly NavItemId[]): {
  bar: NavItemId[];
  menu: NavItemId[];
} {
  const bar = NAV_BAR.filter((id) => present.includes(id));
  const menu = NAV_MENU_ORDER.filter(
    (id) => present.includes(id) && !bar.includes(id),
  );
  return { bar, menu };
}
