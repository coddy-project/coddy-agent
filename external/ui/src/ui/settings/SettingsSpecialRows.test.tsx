import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type {
  Owned,
  SettingsBlock,
  SettingsBlockContext,
} from "../extensions/contributions";
import {
  SettingsRowAlterContext,
  type SettingsRowAlter,
} from "../extensions/settingsRowAlter";
import { extensionThemeRegistry } from "../extensions/themeRegistry";
import { initLocale } from "../i18n/i18n";
import { resetTelegramLaunchForTests } from "../telegram/launch";
import { launchAsTelegram } from "./settingsServer.fakes";
import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { ProviderModelList } from "./ProviderModelList";
import type { JsonSchema } from "./SchemaForm";
import { buildSettingsSearchIndex, searchSettings } from "./settingsSearch";
import { SETTINGS_LANDMARKS, type SettingsLandmark } from "./settingsLandmarks";
import { buildSpecialRowPlan } from "./SettingsSpecialRows";
import { deriveSettingsSections, landmarkAnchor } from "./settingsSections";

const api = {} as Owned<SettingsBlock>["api"];

function ownedBlock(
  value: Pick<SettingsBlock, "id" | "section" | "title" | "mount"> &
    Partial<SettingsBlock>,
): Owned<SettingsBlock> {
  return {
    ...value,
    key: `demo/${value.id}`,
    owner: "demo",
    api,
  };
}

function alter(id: string, run: SettingsRowAlter["run"]): SettingsRowAlter {
  return { id, run };
}

function specialAnchors(index: ReturnType<typeof buildSettingsSearchIndex>) {
  return index
    .filter((entry) => entry.anchor?.startsWith("landmark:"))
    .map((entry) => entry.anchor);
}

beforeEach(() => {
  initLocale("en");
  resetTelegramLaunchForTests();
});

afterEach(() => {
  cleanup();
  extensionThemeRegistry.clear();
  resetTelegramLaunchForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("special Settings rows", () => {
  test("Appearance hides, retitles and reorders identically in drawing and search", async () => {
    const dispose = vi.fn();
    const extra = ownedBlock({
      id: "extra",
      section: "appearance",
      title: "Extra appearance",
      description: "Drawn by the extension",
      mount(element) {
        element.textContent = "extra body";
        return dispose;
      },
    });
    const rank = new Map([
      ["notifications", 0],
      ["ext:demo/extra", 1],
      ["language", 2],
      ["theme", 3],
    ]);
    const passes = [
      alter("appearance-test", (rows) =>
        rows
          .map((row) => ({
            ...row,
            visible: row.id === "theme" ? false : row.visible,
            label:
              row.id === "language"
                ? "Language renamed"
                : row.id === "ext:demo/extra"
                  ? "Extra renamed"
                  : row.label,
          }))
          .sort((a, b) => (rank.get(a.id) ?? 100) - (rank.get(b.id) ?? 100)),
      ),
    ];

    const { container, unmount } = render(
      <SettingsRowAlterContext.Provider value={{ alters: passes }}>
        <AppearanceThemePicker settingsBlocks={[extra]} />
      </SettingsRowAlterContext.Provider>,
    );

    const body = screen.getByTestId("appearance-theme-picker");
    expect(
      [...body.children].map((child) =>
        child.getAttribute("data-settings-anchor"),
      ),
    ).toEqual([
      landmarkAnchor("appearance", "notifications"),
      landmarkAnchor("appearance", "ext:demo/extra"),
      landmarkAnchor("appearance", "language"),
    ]);
    expect(
      body.querySelector('[data-settings-anchor="landmark:appearance:theme"]'),
    ).toBeNull();
    expect(screen.getByText("Language renamed")).toBeInTheDocument();
    expect(screen.getByText("Extra renamed")).toBeInTheDocument();
    expect(screen.getByText("extra body")).toBeInTheDocument();
    expect(screen.queryByTestId("theme-swatch-dark")).toBeNull();

    const sections = deriveSettingsSections(null).filter(
      (section) => section.id === "appearance",
    );
    const index = buildSettingsSearchIndex({
      sections,
      schema: null,
      doc: {},
      rowAlters: passes,
      blocks: [extra],
    });
    expect(specialAnchors(index)).toEqual([
      landmarkAnchor("appearance", "notifications"),
      landmarkAnchor("appearance", "ext:demo/extra"),
      landmarkAnchor("appearance", "language"),
    ]);
    expect(
      searchSettings(index, "Language renamed").hits.map(
        (hit) => hit.entry.names[0],
      ),
    ).toEqual(["Language renamed"]);
    expect(
      index.some(
        (entry) => entry.kind === "landmark" && entry.names[0] === "Theme",
      ),
    ).toBe(false);
    expect(
      searchSettings(index, "Extra renamed").hits.map(
        (hit) => hit.entry.names[0],
      ),
    ).toEqual(["Extra renamed"]);

    const mountedView = container.querySelector(
      ".extension-settings-block-view",
    )!;
    unmount();
    expect(dispose).toHaveBeenCalledTimes(1);
    expect(mountedView.childNodes).toHaveLength(0);
  });

  test("a failing alter keeps the earlier special-row answer and ignores unknown or duplicate ids", () => {
    const landmarks = SETTINGS_LANDMARKS.filter(
      (landmark) => landmark.section === "appearance",
    );
    const passes = [
      alter("first", (rows) => [
        ...rows.slice(1).map((row) => ({ ...row, label: `First ${row.id}` })),
        rows[0]!,
      ]),
      alter("broken", () => {
        throw new Error("broken pass");
      }),
      alter("unknown", (rows) => [
        { ...rows[0]!, id: "not-registered" },
        rows[0]!,
        ...rows.slice(1),
        rows[0]!,
      ]),
    ];
    const plan = buildSpecialRowPlan({
      section: "appearance",
      doc: {},
      landmarks,
      alters: passes,
    });
    expect(plan.map((item) => item.descriptor.id)).toEqual([
      "language",
      "notifications",
      "theme",
    ]);
    expect(plan.map((item) => item.descriptor.label)).toEqual([
      "First language",
      "First notifications",
      "Theme",
    ]);
  });

  test("provider-row blocks receive only sanitized row metadata in matching, mounting and search", async () => {
    const provider = {
      name: "neuraldeep",
      type: "neuraldeep",
      api_base: "https://example.invalid/v1",
      api_key: "sk-private",
      api_key_command: "private-command",
      token: "private-token",
      nested: { password: "private-password", env: { PRIVATE: "1" }, safe: 7 },
    };
    const contexts: SettingsBlockContext[] = [];
    const mounted = vi.fn(
      (element: HTMLElement, context: SettingsBlockContext) => {
        element.textContent = `provider extra: ${context.rowName ?? "none"}`;
      },
    );
    const providerBlock = ownedBlock({
      id: "provider-extra",
      section: "providers",
      title: "Provider extra",
      match(context) {
        contexts.push(context);
        return (
          context.rowName === "neuraldeep" &&
          context.row?.["type"] === "neuraldeep"
        );
      },
      mount(element, _api, context) {
        contexts.push(context);
        mounted(element, context);
      },
    });
    const throwing = ownedBlock({
      id: "throwing-match",
      section: "providers",
      title: "Must stay hidden",
      match() {
        throw new Error("no match");
      },
      mount: vi.fn(),
    });
    const fetchMock = vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: [] }),
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderModelList
        provider={provider}
        doc={{ providers: [provider] }}
        rowName="neuraldeep"
        existingModels={[]}
        onAddModel={() => {}}
        onRemoveModel={() => {}}
        settingsBlocks={[providerBlock, throwing]}
      />,
    );

    expect(screen.getByText("provider extra: neuraldeep")).toBeInTheDocument();
    expect(screen.queryByText("Must stay hidden")).toBeNull();
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(mounted).toHaveBeenCalledTimes(1);
    for (const context of contexts) {
      expect(Object.isFrozen(context)).toBe(true);
      expect(context.rowName).toBe("neuraldeep");
      expect(Object.isFrozen(context.row)).toBe(true);
      expect(context.row).toMatchObject({
        name: "neuraldeep",
        type: "neuraldeep",
        api_base: "https://example.invalid/v1",
        nested: { safe: 7 },
      });
      expect(context.row).not.toHaveProperty("api_key");
      expect(context.row).not.toHaveProperty("api_key_command");
      expect(context.row).not.toHaveProperty("token");
      expect(context.row?.["nested"]).not.toHaveProperty("password");
      expect(context.row?.["nested"]).not.toHaveProperty("env");
    }

    const schema: JsonSchema = {
      type: "object",
      properties: {
        providers: {
          type: "array",
          items: {
            type: "object",
            properties: {
              name: { type: "string" },
              type: { type: "string" },
            },
          },
        },
      },
    };
    const index = buildSettingsSearchIndex({
      sections: deriveSettingsSections(schema),
      schema,
      doc: { providers: [provider] },
      blocks: [providerBlock, throwing],
    });
    const matches = index.filter(
      (entry) =>
        entry.anchor === landmarkAnchor("providers", "ext:demo/provider-extra"),
    );
    expect(matches).toHaveLength(1);
    expect(matches[0]).toMatchObject({
      row: "neuraldeep",
      names: ["Provider extra"],
      kind: "block",
    });
    expect(
      searchSettings(index, "Provider extra").hits.map((hit) => hit.entry.row),
    ).toEqual(["neuraldeep"]);
    expect(
      index.some((entry) => entry.names.includes("Must stay hidden")),
    ).toBe(false);
  });

  test("an invisible landmark cannot be reintroduced by an alter", () => {
    const notifications = SETTINGS_LANDMARKS.find(
      (landmark) => landmark.id === "notifications",
    ) as SettingsLandmark;
    launchAsTelegram();
    const plan = buildSpecialRowPlan({
      section: "appearance",
      doc: {},
      landmarks: [notifications],
      alters: [
        alter("restore", () => [
          {
            section: "appearance",
            id: "notifications",
            path: "notifications",
            label: "Not in Telegram",
            kind: "block",
            type: "landmark",
          },
        ]),
      ],
    });
    expect(plan).toEqual([]);
  });
});
