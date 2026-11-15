import { expect, test } from "vitest";
import { messagesRu } from "./messages/ru";

// The project's Russian wording (.claude/rules/russian-wording.md): the adjective
// for agent is "агентный", an agent a session spawns is a "субагент", a git
// worktree keeps its English name, and cache is spelled "кеш". messagesParity only
// compares keys, so a wrong word in a value would pass it unnoticed. The patterns
// spell forbidden words with a character class, so the plain `git grep` checks
// find no literal forbidden spellings under external/ui, this file included.
const FORBIDDEN: Array<{ pattern: RegExp; instead: string }> = [
  { pattern: /агентс[к]/i, instead: "агентный" },
  { pattern: /с[а]багент/i, instead: "субагент" },
  { pattern: /рабоч\S*\s+дерев/i, instead: "worktree" },
  { pattern: /к[э]ш/i, instead: "кеш" },
];

test("the Russian dictionary keeps the project's wording", () => {
  const hits: string[] = [];
  for (const [key, value] of Object.entries(messagesRu)) {
    for (const { pattern, instead } of FORBIDDEN) {
      if (pattern.test(value)) {
        hits.push(`${key}: "${value}" (write ${instead})`);
      }
    }
  }
  expect(hits).toEqual([]);
});
