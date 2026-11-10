import { describe, expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

function ruleBody(selector: string): string {
  const source = stylesPartial("settings/fields").replace(/\s+/g, " ");
  const match = source.match(
    new RegExp(
      `${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([^}]*)\\}`,
    ),
  );
  if (!match) throw new Error(`Missing rule ${selector}`);
  return match[1]!;
}

describe("authentication settings layout", () => {
  test("keeps account fields in two desktop columns and one phone column", () => {
    expect(ruleBody(".auth-account-fields")).toContain(
      "grid-template-columns: repeat(2, minmax(0, 1fr))",
    );
    const source = stylesPartial("settings/fields").replace(/\s+/g, " ");
    expect(source).toMatch(
      /@media \(max-width: 599px\) \{[^]*\.auth-account-fields \{ grid-template-columns: 1fr;/,
    );
  });

  test("places the secret visibility control inside the input without overflowing its grid track", () => {
    expect(ruleBody(".settings-secret-input")).toContain("position: relative");
    expect(ruleBody(".settings-secret-input")).toContain("min-width: 0");
    expect(ruleBody(".settings-secret-input > .settings-input")).toContain(
      "min-width: 0",
    );
    expect(ruleBody(".settings-secret-input > .settings-input")).toContain(
      "padding-right: 40px",
    );
    expect(ruleBody(".settings-secret-input > .settings-btn-icon")).toContain(
      "position: absolute",
    );
    expect(ruleBody(".settings-secret-input > .settings-btn-icon")).toContain(
      "transform: translateY(-50%)",
    );
    expect(ruleBody(".auth-enrollment-fields > .settings-row")).toContain(
      "min-width: 0",
    );
  });

  test("floats the eye over the input and spaces TOTP actions", () => {
    expect(ruleBody(".settings-secret-input > .settings-btn-icon")).toContain(
      "background: transparent",
    );
    expect(ruleBody(".settings-secret-input > .settings-btn-icon")).toContain(
      "border: 0",
    );
    expect(ruleBody(".settings-secret-input > .settings-btn-icon")).toContain(
      "z-index: 1",
    );
    expect(ruleBody(".auth-enrollment-actions")).toContain("display: flex");
    expect(ruleBody(".auth-enrollment-actions")).toContain("gap: 8px");
    expect(ruleBody(".auth-enrollment-actions")).toContain("margin-top: 12px");
  });
});
