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
