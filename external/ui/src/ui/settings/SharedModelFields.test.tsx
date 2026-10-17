import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";
import {
  CoddyAPIBaseField,
  SharedAsField,
  SharedSubscriptionAckField,
} from "./SharedModelFields";

beforeEach(() => initLocale("en"));

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.restoreAllMocks();
});

const INVALID_ALIAS =
  "Not a valid alias: use 1 to 64 letters, digits, dots, underscores or hyphens, starting with a letter or a digit.";

function drawAlias(value: unknown, onChange = vi.fn()) {
  render(
    <I18nProvider>
      <SharedAsField value={value} onChange={onChange} label="Shared as" />
    </I18nProvider>,
  );
  return onChange;
}

function aliasInput(): HTMLInputElement {
  return screen.getByTestId("shared-as-input") as HTMLInputElement;
}

test("an empty or valid alias is shown without an error", () => {
  for (const value of [undefined, null, "", "terra", " qwen3.8-27b "]) {
    drawAlias(value);
    expect(screen.queryByTestId("shared-as-error"), String(value)).toBeNull();
    expect(aliasInput().hasAttribute("aria-invalid")).toBe(false);
    expect(aliasInput().hasAttribute("aria-describedby")).toBe(false);
    cleanup();
  }
});

test("an invalid alias is flagged and the field points at the explanation", () => {
  for (const value of ["team/terra", "-terra", "my model", "a".repeat(65)]) {
    drawAlias(value);
    const error = screen.getByTestId("shared-as-error");
    expect(error.textContent, value).toBe(INVALID_ALIAS);
    expect(error.classList.contains("settings-error")).toBe(true);
    expect(aliasInput().getAttribute("aria-invalid")).toBe("true");
    expect(aliasInput().getAttribute("aria-describedby")).toBe(error.id);
    expect(error.id).not.toBe("");
    cleanup();
  }
});

test("the alias field shows the stored alias and hands on what is typed", () => {
  const onChange = drawAlias("terra");
  expect(aliasInput().value).toBe("terra");
  expect(screen.getByLabelText("Shared as")).toBe(aliasInput());

  fireEvent.change(aliasInput(), { target: { value: "luna" } });
  expect(onChange).toHaveBeenCalledWith("luna");
  // A value is handed on as typed: the trim is the server's, not the field's.
  fireEvent.change(aliasInput(), { target: { value: " sol " } });
  expect(onChange).toHaveBeenLastCalledWith(" sol ");
});

function drawAck(checked: boolean, onChange = vi.fn()) {
  render(
    <I18nProvider>
      <SharedSubscriptionAckField
        checked={checked}
        onChange={onChange}
        label="I accept the risk of sharing a subscription"
        providerName="my-codex"
        providerType="codex"
      />
    </I18nProvider>,
  );
  return onChange;
}

function ackSwitch(): HTMLElement {
  return screen.getByTestId("shared-subscription-ack");
}

function ackWarning(): HTMLElement {
  return screen.getByTestId("shared-ack-warning");
}

test("the acknowledgement warns about the provider by name and type and is marked required", () => {
  drawAck(false);

  expect(ackWarning().textContent).toBe(
    "my-codex (codex) runs on a subscription login. Sharing a login hands its quota to every holder of a shared-model token and may breach the vendor's terms of service. The configuration is refused without this.",
  );
  expect(
    screen.getByTestId("shared-ack").querySelector(".settings-required-mark")
      ?.textContent,
  ).toBe("required");
  expect(
    screen.getByRole("switch", {
      name: "I accept the risk of sharing a subscription required",
    }),
  ).toBe(ackSwitch());
});

test("the warning is an error until the switch is on", () => {
  drawAck(false);
  expect(ackSwitch().getAttribute("aria-checked")).toBe("false");
  expect(ackWarning().classList.contains("settings-error")).toBe(true);
  cleanup();

  drawAck(true);
  expect(ackSwitch().getAttribute("aria-checked")).toBe("true");
  expect(ackWarning().classList.contains("settings-error")).toBe(false);
  // The warning stays on screen once acknowledged: it is what was accepted.
  expect(ackWarning().textContent).toContain("my-codex (codex)");
});

test("the acknowledgement switch writes its new state", () => {
  const off = drawAck(false);
  fireEvent.click(ackSwitch());
  expect(off).toHaveBeenCalledWith(true);
  cleanup();

  const on = drawAck(true);
  fireEvent.click(ackSwitch());
  expect(on).toHaveBeenCalledWith(false);
});

test("the remote's address is a URL field that hands on what is typed", () => {
  const onChange = vi.fn();
  render(
    <I18nProvider>
      <CoddyAPIBaseField
        value="https://terra:12345"
        onChange={onChange}
        label="API base URL"
      />
    </I18nProvider>,
  );

  const input = screen.getByTestId("coddy-api-base") as HTMLInputElement;
  expect(input.inputMode).toBe("url");
  expect(input.value).toBe("https://terra:12345");
  expect(input.placeholder).toBe("https://host:12345");
  expect(screen.getByLabelText("API base URL")).toBe(input);

  fireEvent.change(input, { target: { value: "https://luna:443" } });
  expect(onChange).toHaveBeenCalledWith("https://luna:443");
});

test("the remote's address field is empty for an absent value", () => {
  render(
    <I18nProvider>
      <CoddyAPIBaseField
        value={undefined}
        onChange={vi.fn()}
        label="API base URL"
      />
    </I18nProvider>,
  );
  expect((screen.getByTestId("coddy-api-base") as HTMLInputElement).value).toBe(
    "",
  );
});
