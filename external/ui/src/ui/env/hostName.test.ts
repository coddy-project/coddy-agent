import { expect, test } from "vitest";
import { shortHostName } from "./hostName";

// Issue #357: the local environment is named by the machine it runs on, in the
// short form a person calls it by.
test.each([
  ["devbox.example.org", "devbox"],
  ["devbox", "devbox"],
  ["  build-01.example.org.  ", "build-01"],
  ["192.168.1.10", "192.168.1.10"],
  ["fe80::1", "fe80::1"],
  ["", ""],
])("shortHostName(%j) = %j", (host, want) => {
  expect(shortHostName(host)).toBe(want);
});
