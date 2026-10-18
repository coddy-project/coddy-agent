import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { HeroFooter, resetServerVersionForTests } from "./HeroFooter";
import { connectRemote, setEnv } from "../env/remoteEnv";

// The start screen's footer names the version of the server the page talks to:
// the local one, a remote, or a node reached through a relay. The request goes
// through the environment shim like every other API call, so the global fetch
// is what answers here.
function answer(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input) === "/coddy/info"
        ? new Response(JSON.stringify(body), {
            status,
            headers: { "Content-Type": "application/json" },
          })
        : new Response("", { status: 404 }),
    ),
  );
}

describe("HeroFooter", () => {
  beforeEach(() => resetServerVersionForTests());
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("puts the running server's version after the links, linked to its release", async () => {
    answer({ object: "coddy.info", version: "1.2.35", hostname: "box" });
    const { container } = render(<HeroFooter />);
    const version = await screen.findByRole("link", { name: "v1.2.35" });
    expect(version).toHaveAttribute(
      "href",
      "https://github.com/coddy-project/coddy-agent/releases/tag/1.2.35",
    );
    const items = [...container.querySelectorAll(".hero-footer > *")].map(
      (el) => el.textContent,
    );
    expect(items).toEqual(["GitHub", "|", "API docs", "|", "v1.2.35"]);
  });

  it("shows a build that is not a release as text, without a link", async () => {
    answer({ version: "1.2.31-2-gcb322c30-dirty" });
    render(<HeroFooter />);
    expect(await screen.findByText("v1.2.31-2-gcb322c30-dirty")).toBeTruthy();
    expect(screen.queryByRole("link", { name: /1\.2\.31/ })).toBeNull();
  });

  // A switch between two remotes starts the app over in place, and the new
  // server is asked which build it runs.
  it("asks the new server after a switch in place", async () => {
    const realLocation = window.location;
    Object.defineProperty(window, "location", {
      value: { hash: "", reload: vi.fn() },
      writable: true,
      configurable: true,
    });
    try {
      setEnv({ mode: "remote", baseUrl: "http://a:1", token: "t" });
      answer({ version: "1.2.35" });
      const first = render(<HeroFooter />);
      await screen.findByRole("link", { name: "v1.2.35" });
      first.unmount();
      connectRemote("http://b:1", "t", "b");
      answer({ version: "1.2.36" });
      render(<HeroFooter />);
      expect(await screen.findByRole("link", { name: "v1.2.36" })).toBeTruthy();
    } finally {
      setEnv({ mode: "local" });
      Object.defineProperty(window, "location", {
        value: realLocation,
        writable: true,
        configurable: true,
      });
    }
  });

  // A token rotated in the configuration starts the app over with the new one
  // (EnvScope) without a switch; a version the old token failed to read must
  // not stay cached for the new one.
  it("asks again with a rotated token after a read that failed", async () => {
    setEnv({ mode: "remote", baseUrl: "http://a:1", token: "stale" });
    answer({}, 401);
    const first = render(<HeroFooter />);
    await new Promise((r) => setTimeout(r, 0));
    expect(first.container.querySelector(".hero-footer-version")).toBeNull();
    first.unmount();
    setEnv({ mode: "remote", baseUrl: "http://a:1", token: "fresh" });
    answer({ version: "1.2.37" });
    render(<HeroFooter />);
    expect(await screen.findByRole("link", { name: "v1.2.37" })).toBeTruthy();
    setEnv({ mode: "local" });
  });

  // Choosing the remote the page is on again is how everything is read again,
  // the version included: the server behind the same address may have been
  // upgraded meanwhile.
  it("asks again when the same remote is chosen again", async () => {
    const realLocation = window.location;
    Object.defineProperty(window, "location", {
      value: { hash: "", reload: vi.fn() },
      writable: true,
      configurable: true,
    });
    try {
      setEnv({ mode: "remote", baseUrl: "http://a:1", token: "t" });
      answer({ version: "1.2.35" });
      const first = render(<HeroFooter />);
      await screen.findByRole("link", { name: "v1.2.35" });
      first.unmount();
      connectRemote("http://a:1", "t", "a");
      answer({ version: "1.2.36" });
      render(<HeroFooter />);
      expect(await screen.findByRole("link", { name: "v1.2.36" })).toBeTruthy();
    } finally {
      setEnv({ mode: "local" });
      Object.defineProperty(window, "location", {
        value: realLocation,
        writable: true,
        configurable: true,
      });
    }
  });

  // A read that got no version is not kept: the next footer asks again.
  it("asks again after a read that got no version", async () => {
    answer({}, 503);
    const first = render(<HeroFooter />);
    await new Promise((r) => setTimeout(r, 0));
    expect(first.container.querySelector(".hero-footer-version")).toBeNull();
    first.unmount();
    answer({ version: "1.2.36" });
    render(<HeroFooter />);
    expect(await screen.findByRole("link", { name: "v1.2.36" })).toBeTruthy();
  });

  it("keeps the two links alone when the server does not say", async () => {
    answer({}, 404);
    const { container } = render(<HeroFooter />);
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelectorAll(".hero-footer-sep")).toHaveLength(1);
  });
});
