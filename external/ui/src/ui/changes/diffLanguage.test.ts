import { describe, expect, test } from "vitest";
import { everyMappedLanguage, languageForPath } from "./diffLanguage";
import { isHighlightable } from "./highlightLine";

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

describe("the wider table", () => {
  test.each([
    ["lib/main.dart", "dart"],
    ["mix/app.ex", "elixir"],
    ["scripts/run.exs", "elixir"],
    ["src/Main.hs", "haskell"],
    ["build.sbt", "scala"],
    ["src/App.scala", "scala"],
    ["tools/setup.ps1", "powershell"],
    ["init.lua", "lua"],
    ["analysis.R", "r"],
    ["lib/Tool.pm", "perl"],
    ["schema.graphql", "graphql"],
    ["api/v1/service.proto", "protobuf"],
    ["build.gradle", "groovy"],
    ["run.bat", "dos"],
    ["shaders/light.frag", "glsl"],
    ["src/lib.ml", "ocaml"],
    ["src/Program.fs", "fsharp"],
    ["kernel.cu", "cpp"],
    ["include/vec.hpp", "cpp"],
    ["App.vue", "xml"],
    ["page.xhtml", "xml"],
    ["tsconfig.jsonc", "json"],
    ["styles.mk", "makefile"],
  ])("maps %s to %s", (path, language) => {
    expect(languageForPath(path)).toBe(language);
  });

  test.each([
    ["Dockerfile.dev", "dockerfile"],
    ["Containerfile", "dockerfile"],
    ["CMakeLists.txt", "cmake"],
    ["Jenkinsfile", "groovy"],
    ["Gemfile", "ruby"],
    ["Rakefile", "ruby"],
    [".bashrc", "bash"],
    [".env", "ini"],
    [".env.local", "ini"],
  ])("knows the file named %s", (path, language) => {
    expect(languageForPath(path)).toBe(language);
  });

  // A name the table hands the highlighter must be a grammar it has: a name it
  // does not know leaves the file plain without a word (Dockerfile once did).
  test("every language the table names is one the highlighter knows", () => {
    const missing = everyMappedLanguage().filter((l) => !isHighlightable(l));
    expect(missing).toEqual([]);
  });
});
