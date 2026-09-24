import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SkillsSection } from "./SkillsSection";
import type { JsonSchema } from "./SchemaForm";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const skillsSchema = {
  type: "object",
  title: "Skills",
  properties: {
    dirs: {
      type: "array",
      title: "Skill directories",
      items: { type: "string" },
    },
    sources: {
      type: "array",
      title: "Remote skill sources",
      items: { type: "string" },
    },
    auto_discovery: {
      type: "boolean",
      title: "Skill auto-discovery",
      description: "Let the agent load a matching skill on its own.",
    },
  },
} as unknown as JsonSchema;

test("auto-discovery is the first fieldset and toggling flips the config value", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) }),
  );
  const onChange = vi.fn();
  render(
    <SkillsSection
      schema={skillsSchema}
      value={{ auto_discovery: true }}
      onChange={onChange}
    />,
  );

  // The auto-discovery fieldset sits at the very top of the Skills group.
  const legends = Array.from(
    document.querySelectorAll(".settings-skills-section > fieldset > legend"),
  ).map((l) => l.textContent);
  expect(legends[0]).toBe("Skill auto-discovery");

  const sw = screen.getByTestId("skills-auto-discovery-toggle");
  expect(sw.getAttribute("aria-checked")).toBe("true");
  // It renders as a switch, not a raw checkbox.
  expect(document.querySelector('input[type="checkbox"]')).toBeNull();

  fireEvent.click(sw);
  expect(onChange).toHaveBeenCalledWith(
    expect.objectContaining({ auto_discovery: false }),
  );
});

// The auto-discovery row is the shared SwitchField, so its state label sits
// level with the switch and its description in the label column, like every
// other boolean in the settings forms.
test("auto-discovery toggle is rendered by the shared SwitchField", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) }),
  );
  render(
    <SkillsSection
      schema={skillsSchema}
      value={{ auto_discovery: true }}
      onChange={() => {}}
    />,
  );
  const sw = screen.getByTestId("skills-auto-discovery-toggle");
  const field = sw.closest(".settings-switch-field");
  expect(field).not.toBeNull();
  expect(
    field!.querySelector(".settings-switch-field-label")?.textContent,
  ).toBe("Enabled");
  // The schema description is the (i) beside the state label, not a
  // paragraph flush with the fieldset edge. Its copy comes from the i18n
  // dictionary (schemaFieldDesc), so pin the stable opening words.
  const hint = field!.querySelector(".field-hint");
  expect(hint).not.toBeNull();
  fireEvent.mouseEnter(hint!);
  expect(screen.getByRole("tooltip").textContent).toMatch(
    /^Let the agent load a matching skill/,
  );
  expect(
    document.querySelectorAll(
      ".settings-skills-section > fieldset:first-of-type .settings-field-desc",
    ).length,
  ).toBe(0);
});

// How else a skill gets installed (npx skills, npx skillsbd) is about the
// whole list: it is the (i) of the Installed skills legend, not a line in it.
test("the npx install hint is the (i) of the Installed skills legend", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) }),
  );
  render(
    <SkillsSection schema={skillsSchema} value={{}} onChange={() => {}} />,
  );
  const box = screen.getByTestId("skills-installed");
  expect(box.querySelector("p")?.textContent ?? "").not.toMatch(/npx skills/);
  const hint = box.querySelector("legend .field-hint")!;
  expect(hint).toHaveAttribute("aria-label", "About Installed skills");
  fireEvent.mouseEnter(hint);
  expect(screen.getByRole("tooltip").textContent).toMatch(/npx skills/);
});

// An installed skill row: the on/off switch first, then the name, no icon.
test("an installed skill row leads with its switch", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) =>
      Promise.resolve({
        ok: true,
        json: async () =>
          String(url).startsWith("/coddy/skills?") ||
          String(url) === "/coddy/skills"
            ? {
                items: [
                  {
                    name: "rpa-feat",
                    description: "BDD workflow",
                    enabled: true,
                    version: "1.0.1",
                  },
                ],
              }
            : { items: [] },
      }),
    ),
  );
  render(
    <SkillsSection schema={skillsSchema} value={{}} onChange={() => {}} />,
  );
  const toggle = await screen.findByTestId("skills-toggle-rpa-feat");
  const row = toggle.closest("li")!;
  expect(row.firstElementChild).toBe(toggle);
  expect(row.querySelector(":scope > svg")).toBeNull();
  expect(row.textContent).toContain("v1.0.1");
});
