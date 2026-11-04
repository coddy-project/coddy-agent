import { describe, expect, it } from "vitest";
import { stylesPartial } from "../../stylesSource";

/** The body of one rule of the onboarding partial, whitespace squashed, so a
 * declaration Prettier broke over several lines still reads as one. The
 * selector is matched where it opens a rule of its own, so asking for
 * `.provider-picker-secondary` does not return the body of the pair rule
 * `.provider-picker-primary, .provider-picker-secondary`. */
function rule(selector: string): string {
  const css = stylesPartial("onboarding");
  // A blank line in front: the partial separates its rules with one, and a
  // selector that only continues a list sits on the line after a comma.
  const head = new RegExp(
    `(?:^|\\n\\n)${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`,
  );
  const m = head.exec(css);
  expect(m, `${selector} opens no rule in styles/_onboarding.scss`).not.toBe(
    null,
  );
  const from = (m as RegExpExecArray).index + (m as RegExpExecArray)[0].length;
  return css.slice(from, css.indexOf("}", from)).replace(/\s+/g, " ").trim();
}

describe("the provider picker's styles", () => {
  // The actions row wraps below 600px and Save goes to a line of its own,
  // where flex no longer stretches it to the height of Skip and Test. Both
  // kinds of button carry a 1px border, so the three are the same height
  // whether they share a line or not.
  it("gives the primary action a border, so a wrapped Save is not shorter", () => {
    expect(rule(".provider-picker-primary")).toContain(
      "border: 1px solid transparent",
    );
    expect(rule(".provider-picker-secondary")).toMatch(/border: 1px solid /);
    // Both take their box from the one rule that pairs them, so the border
    // is the only thing that could make them differ in height.
    expect(stylesPartial("onboarding")).toMatch(
      /\.provider-picker-primary,\s*\.provider-picker-secondary \{[^}]*padding: 0 14px/,
    );
  });

  // A row with a button beside the field used to stand a few pixels off the
  // row above it: the inputs were 37px, the combobox and the proxy field
  // 34px, and the buttons beside them bare text of 19px. One variable holds
  // every control of the form to one height.
  it("gives every control of the form one height", () => {
    expect(rule(".provider-picker-panel")).toContain(
      "--onboarding-control-h: 36px",
    );
    for (const selector of [
      ".provider-picker-input",
      ".provider-picker-ghost-btn",
      ".provider-picker-primary,\n.provider-picker-secondary",
    ]) {
      const body = stylesPartial("onboarding");
      const at = body.indexOf(`\n\n${selector} {`);
      expect(at, `${selector} opens no rule`).toBeGreaterThan(-1);
      expect(
        body.slice(at, body.indexOf("}", at)).replace(/\s+/g, " "),
      ).toContain("height: var(--onboarding-control-h)");
    }
  });

  // The reveal of the key is inside the field, so the input keeps the full
  // width of the form instead of ending short of every other one.
  it("puts the key's reveal inside the field", () => {
    expect(rule(".provider-picker-secret")).toContain("position: relative");
    const eye = rule(".provider-picker-eye");
    expect(eye).toContain("position: absolute");
    expect(eye).toContain("right: 0");
    expect(eye).toContain("width: var(--onboarding-control-h)");
    expect(rule(".provider-picker-secret .provider-picker-input")).toContain(
      "padding-right: calc(var(--onboarding-control-h) + 2px)",
    );
  });

  // The proxy block is one switch and one field like any other row, so it
  // carries no frame and its input starts where the others do.
  it("takes the frame off the proxy block", () => {
    const body = rule(".provider-picker-proxy > .settings-row");
    expect(body).toContain("padding: 0");
    expect(body).toContain("border: none");
  });

  // One column on the phone tier, two above it: the eight cards of the
  // catalog are a list on a phone and a grid on everything else.
  it("lays the cards out in two columns above the phone tier", () => {
    const css = stylesPartial("onboarding");
    expect(rule(".provider-picker-grid")).toContain(
      "grid-template-columns: repeat(2, minmax(0, 1fr))",
    );
    // A card whose description wraps must not make its row taller than the
    // others.
    expect(rule(".provider-picker-grid")).toContain("grid-auto-rows: 1fr");
    expect(css).toMatch(
      /@media \(max-width: 599px\)[\s\S]*?\.provider-picker-grid \{[^}]*grid-template-columns: 1fr/,
    );
  });
});
