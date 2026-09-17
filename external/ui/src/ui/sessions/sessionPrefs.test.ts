import { describe, expect, it } from "vitest";
import {
  readSessionPref,
  SESSION_PREF_COOKIES,
  writeSessionPref,
} from "./sessionPrefs";

const isColour = (v: string): v is "red" | "blue" =>
  v === "red" || v === "blue";

describe("session preferences", () => {
  it("reads back what was written", () => {
    writeSessionPref(SESSION_PREF_COOKIES.status, "red");
    expect(readSessionPref(SESSION_PREF_COOKIES.status, isColour)).toBe("red");
  });

  it("answers null when nothing was stored", () => {
    document.cookie = `${SESSION_PREF_COOKIES.sort}=; Path=/; Max-Age=0`;
    expect(readSessionPref(SESSION_PREF_COOKIES.sort, isColour)).toBeNull();
  });

  it("refuses a value the code no longer knows", () => {
    // A cookie from an older build, or one edited by hand, must fall back to
    // the default rather than put the drawer in a state that does not exist.
    writeSessionPref(SESSION_PREF_COOKIES.origin, "chartreuse");
    expect(readSessionPref(SESSION_PREF_COOKIES.origin, isColour)).toBeNull();
  });

  it("keeps the settings apart from one another", () => {
    writeSessionPref(SESSION_PREF_COOKIES.status, "red");
    writeSessionPref(SESSION_PREF_COOKIES.sort, "blue");
    expect(readSessionPref(SESSION_PREF_COOKIES.status, isColour)).toBe("red");
    expect(readSessionPref(SESSION_PREF_COOKIES.sort, isColour)).toBe("blue");
  });
});
