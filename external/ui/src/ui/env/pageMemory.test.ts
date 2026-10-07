import { expect, test } from "vitest";
import { rememberSchedulerLinked, schedulerLinkedGuess } from "./pageMemory";

// The guess an app started over by a switch in place begins from, before its
// own server answers. A "no" is taken only from the server that said it: a
// relay has no scheduler by design, and a guessed "no" for a node would drop
// the node's #/scheduler route before the node could say it has one.
test("guesses no scheduler only for a server that said so", () => {
  expect(schedulerLinkedGuess("remote:http://relay:1")).toBeNull();
  rememberSchedulerLinked("remote:http://relay:1", false);
  expect(schedulerLinkedGuess("remote:http://relay:1")).toBe(false);
  expect(
    schedulerLinkedGuess("remote:http://relay:1/swarm/nodes/a"),
  ).toBeNull();
  rememberSchedulerLinked("remote:http://relay:1/swarm/nodes/a", true);
  expect(schedulerLinkedGuess("remote:http://relay:1/swarm/nodes/b")).toBe(
    true,
  );
  expect(schedulerLinkedGuess("remote:http://relay:1")).toBe(false);
});
