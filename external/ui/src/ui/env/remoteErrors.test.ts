import { describe, expect, it } from "vitest";
import type { CoddyEnv } from "./remoteEnv";
import {
  isAbortError,
  remoteHttpErrorMessage,
  remoteSendErrorMessage,
} from "./remoteErrors";

const local: CoddyEnv = { mode: "local" };
const remote: CoddyEnv = {
  mode: "remote",
  baseUrl: "https://box.example:12345",
  token: "",
  name: "box",
};

describe("isAbortError", () => {
  it("is true for a DOMException AbortError", () => {
    expect(isAbortError(new DOMException("aborted", "AbortError"))).toBe(true);
  });
  it("is true for any error whose name is AbortError", () => {
    expect(isAbortError({ name: "AbortError" })).toBe(true);
  });
  it("is false for a network TypeError", () => {
    expect(isAbortError(new TypeError("Failed to fetch"))).toBe(false);
  });
  it("is false for null/undefined", () => {
    expect(isAbortError(null)).toBe(false);
    expect(isAbortError(undefined)).toBe(false);
  });
});

describe("remoteSendErrorMessage (fetch rejected — no Response)", () => {
  it("names the remote host and mentions CORS for a remote env", () => {
    const msg = remoteSendErrorMessage(
      new TypeError("Failed to fetch"),
      remote,
    );
    expect(msg).toContain("box.example:12345");
    expect(msg.toLowerCase()).toMatch(/reach|unreachable/);
    expect(msg.toLowerCase()).toContain("cors");
  });
  it("does not leak the https:// scheme into the host label", () => {
    const msg = remoteSendErrorMessage(
      new TypeError("Failed to fetch"),
      remote,
    );
    expect(msg).not.toContain("https://");
  });
  it("gives a generic network message for local", () => {
    const msg = remoteSendErrorMessage(new TypeError("Failed to fetch"), local);
    expect(msg).not.toContain("CORS");
    expect(msg.toLowerCase()).toContain("network");
  });
});

describe("remoteHttpErrorMessage (readable non-ok Response)", () => {
  it("gives an auth-specific message for 401 on a remote", () => {
    const msg = remoteHttpErrorMessage(401, remote);
    expect(msg).toContain("box.example:12345");
    expect(msg.toLowerCase()).toContain("token");
  });
  it("treats 403 like 401 (auth)", () => {
    expect(remoteHttpErrorMessage(403, remote).toLowerCase()).toContain(
      "token",
    );
  });
  it("keeps a terse message for 401 on local", () => {
    expect(remoteHttpErrorMessage(401, local)).toContain("401");
    expect(remoteHttpErrorMessage(401, local).toLowerCase()).not.toContain(
      "token",
    );
  });
  it("names the remote host for a generic non-ok status", () => {
    const msg = remoteHttpErrorMessage(500, remote);
    expect(msg).toContain("box.example:12345");
    expect(msg).toContain("500");
  });
  it("keeps the legacy terse message for a generic status on local", () => {
    expect(remoteHttpErrorMessage(500, local)).toBe("Request failed (500).");
  });
});
