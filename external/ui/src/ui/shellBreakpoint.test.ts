import { expect, test } from "vitest";
import {
  PHONE_MAX_WIDTH_PX,
  phoneMaxWidthMediaQuery,
  SHELL_STACK_MAX_WIDTH_PX,
  shellStackMaxWidthMediaQuery,
  WIDE_RAIL_MIN_WIDTH_PX,
  wideRailMinWidthMediaQuery,
} from "./shellBreakpoint";

test("shell stack breakpoint matches CSS tier (1199 / 1200)", () => {
  expect(SHELL_STACK_MAX_WIDTH_PX).toBe(1199);
  expect(shellStackMaxWidthMediaQuery).toBe("(max-width: 1199px)");
});

test("the phone tier is the compact window class: up to 599px", () => {
  // 360 to 430 CSS px in portrait (Galaxy S21, iPhone 13 mini, Honor X9d) and
  // whatever else is narrower than a small tablet; 600px and up is a tablet.
  expect(PHONE_MAX_WIDTH_PX).toBe(599);
  expect(phoneMaxWidthMediaQuery).toBe("(max-width: 599px)");
});

test("the wide tier starts at 1920px", () => {
  expect(WIDE_RAIL_MIN_WIDTH_PX).toBe(1920);
  expect(wideRailMinWidthMediaQuery).toBe("(min-width: 1920px)");
});

test("the tiers follow one another without a gap or an overlap", () => {
  expect(PHONE_MAX_WIDTH_PX).toBeLessThan(SHELL_STACK_MAX_WIDTH_PX);
  expect(SHELL_STACK_MAX_WIDTH_PX).toBeLessThan(WIDE_RAIL_MIN_WIDTH_PX);
});
