import { expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

/**
 * The top bar of a phone and a tablet reads History, the Scheduler, the
 * environment, the swarm and then More (DESIGN.md, Layout, the top bar). The
 * rail keeps the environment at its foot, below the swarm, so the row is
 * ordered rather than mirrored in the markup: these three rules are what puts
 * the environment before the swarm and More last. jsdom paints nothing, so a
 * vitest that renders NavRail cannot see the order these rules give.
 */

const stacked = stylesPartial("shell/stacked");

function ruleBody(selector: string): string {
  const idx = stacked.indexOf(`${selector} {`);
  expect(
    idx,
    `${selector} is missing from the stacked shell partial`,
  ).toBeGreaterThan(-1);
  const open = stacked.indexOf("{", idx);
  return stacked
    .slice(open + 1, stacked.indexOf("}", open))
    .replace(/\s+/g, " ");
}

test("the environment stands third in the phone bar, before the swarm", () => {
  expect(ruleBody(".rail-middle > .rail-env-host")).toContain("order: 3");
});

test("the swarm follows the environment", () => {
  expect(
    ruleBody('.rail-middle > .rail-tip-host:has([data-testid="nav-swarm"])'),
  ).toContain("order: 4");
});

test("More is the last control of the bar", () => {
  expect(ruleBody(".rail-middle > .rail-more-host")).toContain("order: 5");
});
