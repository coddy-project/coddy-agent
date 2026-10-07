import { describe, expect, test } from "vitest";
import { languageForPath } from "./diffLanguage";

describe("languageForPath", () => {
  test("maps the extensions the agent edits most", () => {
    expect(languageForPath("external/ui/src/ui/App.tsx")).toBe("typescript");
    expect(languageForPath("internal/session/state.go")).toBe("go");
    expect(languageForPath("scripts/build.py")).toBe("python");
    expect(languageForPath("src/styles.css")).toBe("css");
    expect(languageForPath("config.yaml")).toBe("yaml");
    expect(languageForPath("package.json")).toBe("json");
  });

  test("handles Windows separators, which the session scope reports", () => {
    expect(languageForPath(".idea\\workspace.xml")).toBe("xml");
  });

  test("is case insensitive about the extension", () => {
    expect(languageForPath("Main.GO")).toBe("go");
    expect(languageForPath("READ.MD")).toBe("markdown");
  });

  test("recognises the extensionless names that are still code", () => {
    expect(languageForPath("Dockerfile")).toBe("dockerfile");
    expect(languageForPath("build/Makefile")).toBe("makefile");
  });

  test("says nothing rather than guessing for unknown files", () => {
    expect(languageForPath("notes.txt")).toBe("");
    expect(languageForPath("LICENSE")).toBe("");
    expect(languageForPath("archive.bin")).toBe("");
    expect(languageForPath("")).toBe("");
  });

  test("uses the last extension of a multi-part name", () => {
    expect(languageForPath("vite.config.ts")).toBe("typescript");
    expect(languageForPath("docker-compose.override.yml")).toBe("yaml");
  });
});
