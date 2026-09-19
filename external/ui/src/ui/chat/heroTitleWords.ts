/** Verbs for the empty-state hero line "What do you want to …?" */

export const HERO_ACCENT_VERBS = [
  "know",
  "build",
  "find",
  "research",
  "explore",
  "debug",
  "ship",
  "design",
  "learn",
  "automate",
  "refactor",
  "plan",
] as const;

export type HeroAccentVerb = (typeof HERO_ACCENT_VERBS)[number];

export function hashString(s: string): number {
  let h = 5381;
  for (let i = 0; i < s.length; i++) {
    h = (h * 33) ^ s.charCodeAt(i);
  }
  return Math.abs(h);
}

/**
 * Picks the accent verb for the hero title.
 * With an active session id, the choice is stable for that id.
 * On the home empty state (no session), homeGeneration rotates per new chat.
 */
export function pickHeroAccentVerb(
  sessionId: string,
  homeGeneration: number,
): HeroAccentVerb {
  const n = HERO_ACCENT_VERBS.length;
  const sid = sessionId.trim();
  if (sid) {
    return HERO_ACCENT_VERBS[hashString(sid) % n] ?? HERO_ACCENT_VERBS[0];
  }
  const idx = ((homeGeneration % n) + n) % n;
  return HERO_ACCENT_VERBS[idx] ?? HERO_ACCENT_VERBS[0];
}
