import { afterEach, expect, test } from "vitest";
import {
  emitChangesSettled,
  onChangesSettled,
  resetChangesBusForTests,
} from "./sessionChangesBus";

afterEach(() => {
  resetChangesBusForTests();
});

test("the word reaches every listener with its session", () => {
  const a: string[] = [];
  const b: string[] = [];
  const offA = onChangesSettled((sid) => a.push(sid));
  onChangesSettled((sid) => b.push(sid));
  emitChangesSettled("s1");
  offA();
  emitChangesSettled("s2");
  expect(a).toEqual(["s1"]);
  expect(b).toEqual(["s1", "s2"]);
});

test("a listener that throws does not stop the others", () => {
  let reached = false;
  onChangesSettled(() => {
    throw new Error("broken");
  });
  onChangesSettled(() => {
    reached = true;
  });
  emitChangesSettled("s1");
  expect(reached).toBe(true);
});
