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

  // A block the form borrows from the settings screen - an account sign-in -
  // is one row like the fields around it. Framed, it stood 13px further in
  // than the key above it, and its button 2px shorter than the fields.
  it("takes the frame off every block borrowed from the settings screen", () => {
    const body = rule(".provider-picker-form > .settings-row");
    expect(body).toContain("padding: 0");
    expect(body).toContain("border: none");
    expect(body).toContain("background: none");
    expect(
      rule(".provider-picker-form > .settings-row .settings-btn"),
    ).toContain("height: var(--onboarding-control-h)");
  });

  // The names the form writes itself and the ones a borrowed block brings
  // with it stand in one column, so they are one size and one colour; the
  // settings screen's muted grey next to this form's own read as two kinds
  // of field.
  it("gives every label of the form one size and one colour", () => {
    const body = rule(".provider-picker-form .settings-label");
    expect(body).toContain("font-size: 13px");
    expect(body).toContain("color: var(--text)");
    expect(rule(".provider-picker-field")).toContain("font-size: 13px");
  });

  // The model and the button that lists it share a line, except on a phone,
  // where the button left the field about 130px and a model id read as
  // "neuraldeep/q".
  it("gives the model field the line on a phone", () => {
    const css = stylesPartial("onboarding");
    expect(css).toMatch(
      /@media \(max-width: 599px\)[\s\S]*?\.provider-picker-key-row \{[^}]*flex-wrap: wrap/,
    );
    expect(css).toMatch(
      /@media \(max-width: 599px\)[\s\S]*?\.provider-picker-key-row \.settings-combobox,\s*\n\s*\.provider-picker-key-row \.provider-picker-input \{[^}]*flex: 1 1 100%/,
    );
  });

  // Two columns at every width, a phone included: one column there turned
  // eight providers into a scroll of its own before the form was reached, so
  // the description gives way instead of the column.
  it("lays the cards out in two columns at every width", () => {
    const css = stylesPartial("onboarding");
    expect(rule(".provider-picker-grid")).toContain(
      "grid-template-columns: repeat(2, minmax(0, 1fr))",
    );
    // A card whose description wraps must not make its row taller than the
    // others.
    // Fixed rows, so the window below shows whole cards; `1fr` inside a
    // scroller would share the window between every row instead.
    expect(rule(".provider-picker-grid")).toContain(
      "grid-auto-rows: var(--onboarding-card-h)",
    );
    expect(rule(".provider-picker-grid")).toContain("overflow-y: auto");
    expect(css).not.toMatch(
      /@media \(max-width: 599px\)[\s\S]*?\.provider-picker-grid \{[^}]*grid-template-columns: 1fr/,
    );
    expect(css).toMatch(
      /@media \(max-width: 599px\)[\s\S]*?\.provider-picker-card-desc \{[^}]*font-size: 11px/,
    );
  });

  // The catalog is a window over one grid, and the advanced fields are a
  // fieldset of the settings screen's own kind, whose body keeps this form's
  // left edge rather than the indent that screen gives it.
  it("shows the catalog through a window and the advanced fields in a fieldset", () => {
    const grid = rule(".provider-picker-grid");
    expect(grid).toContain("--onboarding-card-rows: 2");
    expect(grid).toMatch(/max-height: calc\(/);
    expect(grid).toContain("overscroll-behavior: contain");
    const css = stylesPartial("onboarding");
    expect(css).not.toContain(".provider-picker-disclosure");
    expect(css).toMatch(
      /@media \(max-width: 599px\)[\s\S]*?--onboarding-card-rows: 4/,
    );
    // The frame stands on the form's edge and the fields keep clear of it by
    // one inset on every side: flush against the frame they read as cramped,
    // and the frame's own block padding stacked on the body's left them 43px
    // from the top against 13px from the sides.
    expect(
      rule(".provider-picker-advanced-fold .settings-fieldset--collapsible"),
    ).toContain("padding: 0");
    expect(
      rule(".provider-picker-advanced-fold .settings-form-group-body"),
    ).toContain("padding: 12px");
    // Both fades are variables the dialog writes from the scroll, not fixed
    // edges that would stay past the first row and the last.
    const grid2 = rule(".provider-picker-grid");
    expect(grid2).toContain("--onboarding-fade-top: 0px");
    expect(grid2).toContain("--onboarding-fade-bottom: 0px");
    expect(grid2).toContain("#000 var(--onboarding-fade-top)");
    expect(grid2).toContain("calc(100% - var(--onboarding-fade-bottom))");
    expect(rule(".provider-picker-advanced")).toContain(
      "flex-direction: column",
    );
  });

  // One rhythm, set in one place. The blocks used to carry a margin each,
  // which added to the form's gap: 32px before the actions while folded and
  // 28px between the advanced row and what it opened.
  it("keeps one vertical rhythm for the whole dialog", () => {
    const panel = rule(".provider-picker-panel");
    expect(panel).toContain("flex-direction: column");
    expect(panel).toContain("gap: 16px");
    expect(rule(".provider-picker-form")).toContain("gap: 12px");
    expect(rule(".provider-picker-field")).toContain("gap: 6px");
    // No block may set a margin of its own beside that gap.
    for (const selector of [
      ".provider-picker-grid",
      ".provider-picker-form",
      ".provider-picker-lead",
    ]) {
      expect(rule(selector), selector).not.toMatch(/margin-bottom: [1-9]/);
    }
  });
});
