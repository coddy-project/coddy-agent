import { describe, expect, it } from "vitest";
import type { CoddyEnv } from "./remoteEnv";
import {
  errorDetail,
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

// The notice is all an operator has for a request that never reached the
// server, so the browser's reason and the server's message ride along.
describe("the reason behind a failed send", () => {
  it("adds the browser's reason to a network failure", () => {
    expect(
      remoteSendErrorMessage(new TypeError("Failed to fetch"), local),
    ).toBe(
      "Network error sending the message — check that the server is running. (Failed to fetch)",
    );
  });
  it("adds nothing when the error carries no reason", () => {
    expect(remoteSendErrorMessage({}, local)).toBe(
      "Network error sending the message — check that the server is running.",
    );
    expect(remoteSendErrorMessage(new TypeError(""), local)).toBe(
      "Network error sending the message — check that the server is running. (TypeError)",
    );
  });
  it("adds the server's message to a refusal by status", () => {
    expect(remoteHttpErrorMessage(413, local, "request body too large")).toBe(
      "Request failed (413). (request body too large)",
    );
    expect(remoteHttpErrorMessage(500, local, "  ")).toBe(
      "Request failed (500).",
    );
  });
  it("keeps the auth hint alone for 401", () => {
    expect(remoteHttpErrorMessage(401, local, "unauthorized")).toBe(
      "Unauthorized (401).",
    );
  });
  it("reads a DOMException's message, then its name", () => {
    expect(
      errorDetail(new DOMException("could not be read", "NotReadableError")),
    ).toBe("could not be read");
    expect(errorDetail(new DOMException("", "NotReadableError"))).toBe(
      "NotReadableError",
    );
    expect(errorDetail(null)).toBe("");
  });
});
