/**
 * Which top bar items fit on a phone and which fold into the More menu.
 * History and swarm never fold; the rest come back into the bar in
 * NAV_RETURN_ORDER as room grows, and the menu lists what is left in
 * NAV_MENU_ORDER, sign-out last.
 */
export type NavItemId =
  | "history"
  | "scheduler"
  | "swarm"
  | "docs"
  | "settings"
  | "signOut";

export const NAV_ALWAYS: readonly NavItemId[] = ["history", "swarm"];
export const NAV_RETURN_ORDER: readonly NavItemId[] = [
  "settings",
  "scheduler",
  "docs",
  "signOut",
];
export const NAV_MENU_ORDER: readonly NavItemId[] = [
  "docs",
  "scheduler",
  "settings",
  "signOut",
];

/**
 * Splits the items the bar has (`present`, in bar order) for a bar with room
 * for `slots` icons. `null` slots means the bar was not measured (a desktop
 * rail, or no layout): everything stays in the bar.
 */
export function splitNavItems(
  present: readonly NavItemId[],
  slots: number | null,
): { bar: NavItemId[]; menu: NavItemId[] } {
  if (slots === null || present.length <= slots) {
    return { bar: [...present], menu: [] };
  }
  const always = present.filter((id) => NAV_ALWAYS.includes(id));
  // The More button takes one of the slots.
  const room = Math.max(0, slots - always.length - 1);
  const back = NAV_RETURN_ORDER.filter((id) => present.includes(id)).slice(0, room);
  const bar = present.filter((id) => always.includes(id) || back.includes(id));
  const menu = NAV_MENU_ORDER.filter((id) => present.includes(id) && !bar.includes(id));
  return { bar, menu };
}

/** How many icons `slot` wide, `gap` apart, fit in `available` pixels. */
export function navSlots(available: number, slot: number, gap: number): number {
  if (slot <= 0 || available <= 0) {
    return 0;
  }
  return Math.max(0, Math.floor((available + gap) / (slot + gap)));
}
