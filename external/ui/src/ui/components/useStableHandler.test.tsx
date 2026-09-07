import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";

import { useStableHandler } from "./useStableHandler";

afterEach(() => cleanup());

function Probe(props: {
  onValue: (v: string) => void;
  seen: Array<(v: string) => void>;
}) {
  const stable = useStableHandler(props.onValue);
  props.seen.push(stable);
  return null;
}

test("wrapper identity survives re-renders while dispatching to the latest handler", () => {
  const first = vi.fn();
  const second = vi.fn();
  const seen: Array<(v: string) => void> = [];

  const { rerender } = render(<Probe onValue={first} seen={seen} />);
  rerender(<Probe onValue={second} seen={seen} />);

  expect(seen.length).toBe(2);
  expect(seen[0]).toBe(seen[1]);

  seen[1]!("hello");
  expect(second).toHaveBeenCalledWith("hello");
  expect(first).not.toHaveBeenCalled();
});

test("return value of the latest handler is passed through", () => {
  const seen: Array<(v: string) => number> = [];
  function Len(props: { seen: Array<(v: string) => number> }) {
    const stable = useStableHandler((v: string) => v.length);
    props.seen.push(stable);
    return null;
  }
  render(<Len seen={seen} />);
  expect(seen[0]!("four")).toBe(4);
});
