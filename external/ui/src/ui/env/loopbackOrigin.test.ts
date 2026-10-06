import { describe, expect, it } from "vitest";
import { isLoopbackOrigin } from "./loopbackOrigin";

// The same table the server's isLoopbackOrigin is held to
// (internal/config/config_test.go), so the hint names the toggle exactly where
// the server would honour it.
describe("isLoopbackOrigin", () => {
  it.each([
    "http://localhost",
    "http://localhost:1",
    "https://LOCALHOST:443",
    "http://127.0.0.1:12345",
    "http://127.1.2.3:80",
    "http://[::1]:5173",
    "http://app.localhost:3000",
  ])("admits %s", (origin) => {
    expect(isLoopbackOrigin(origin)).toBe(true);
  });

  it.each([
    "http://localhost.evil.com",
    "http://evil.com",
    "http://10.0.0.5:12345",
    "http://0.0.0.0:1",
    "ftp://localhost",
    "localhost:12345",
    "null",
    "",
    "http://localhost:5173/path",
    "http://localhost:5173/",
    "http://localhost:5173?q=1",
    "http://localhost:5173#f",
    "http://user@localhost:5173",
  ])("refuses %s", (origin) => {
    expect(isLoopbackOrigin(origin)).toBe(false);
  });
});
