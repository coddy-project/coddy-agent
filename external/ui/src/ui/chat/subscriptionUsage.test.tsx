import React from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { setLocale } from "../i18n/i18n";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";
import { UsageBanner } from "./UsageBanner";
import { UsageSection } from "./UsageSection";
import {
  modelUnlimited,
  summarizeUsage,
  usageProviderTitle,
  usageWindowLabelKey,
  type ProviderUsage,
} from "./providerUsage";

const modelId = "work/model";
const snapshot = (
  extra: { [K in keyof ProviderUsage]?: ProviderUsage[K] | undefined } = {},
): ProviderUsage => {
  const u: ProviderUsage = {
    provider: "work",
    providerType: "codex",
    plan: "plus",
    windows: [
      { id: "codex:0:week", label: "Fast model · week", usedPercent: 85 },
    ],
  };
  const record = u as unknown as Record<string, unknown>;
  for (const [key, value] of Object.entries(extra)) {
    if (value === undefined) delete record[key];
    else record[key] = value;
  }
  return u;
};

afterEach(() => {
  cleanup();
  setLocale("en");
});

test.each([
  ["neuraldeep", "NeuralDeep", "NeuralDeep · work"],
  ["codex", "Codex", "Codex · work"],
  ["devin", "Devin", "Devin · work"],
  ["custom", "work", "work"],
])(
  "aliased %s rows use the provider brand and name the row in the heading",
  (providerType, brand, title) => {
    // Several profiles of one type are told apart by the row in the heading;
    // the banner's sentence keeps the brand alone.
    const usage = snapshot({
      providerType,
      windows: [{ id: "week", label: "week", usedPercent: 85 }],
    });
    const { container } = render(
      <>
        <UsageSection usage={usage} modelId={modelId} />
        <UsageBanner usage={usage} modelId={modelId} />
      </>,
    );
    expect(container.querySelector(".context-usage-title")?.textContent).toBe(
      `${title} · Plus`,
    );
    expect(
      container.querySelector(".usage-banner-text")?.textContent,
    ).toContain(`your ${brand} week limit`);
  },
);

test.each([
  ["neuraldeep", "NeuralDeep"],
  ["codex", "Codex"],
  ["devin", "Devin"],
])(
  "a %s row named after its type is headed by the brand alone",
  (providerType, brand) => {
    expect(
      usageProviderTitle(snapshot({ provider: providerType, providerType })),
    ).toBe(brand);
  },
);

test("percent-only additional windows render and warn without declaring an account block", () => {
  const usage = snapshot();
  expect(summarizeUsage(usage, modelId)).toMatchObject({
    kind: "metered",
    warn: true,
    session: null,
    week: null,
    day: null,
  });
  expect(modelUnlimited(usage, modelId)).toBe(false);
  const { container } = render(
    <>
      <UsageSection usage={usage} modelId={modelId} />
      <UsageBanner usage={usage} modelId={modelId} />
    </>,
  );
  expect(
    container.querySelector(".context-usage-track")?.getAttribute("aria-label"),
  ).toBe("Fast model · week 85%");
  expect(container.querySelector(".context-usage-pct")?.textContent).toBe(
    "85%",
  );
  expect(container.querySelector(".usage-banner-text")?.textContent).toContain(
    "Codex Fast model · week limit",
  );
  expect(
    container.querySelector(".usage-banner")?.getAttribute("data-tone"),
  ).toBe("warn");
  expect(container.textContent).not.toContain("account is blocked");
});

test("warnings include an additional window alongside quiet standard windows", () => {
  const usage = snapshot();
  usage.windows?.unshift({ id: "session", label: "5h", usedPercent: 10 });
  expect(summarizeUsage(usage, modelId)).toMatchObject({
    kind: "metered",
    warn: true,
  });
  const { container } = render(<UsageBanner usage={usage} modelId={modelId} />);
  expect(container.textContent).toContain("85%");
});

test.each(["devin", "codex", "neuraldeep"])(
  "only NeuralDeep hides its zero daily window (%s)",
  (providerType) => {
    const usage = snapshot({
      providerType,
      windows: [{ id: "day", label: "day", usedPercent: 0 }],
    });
    const { container } = render(
      <UsageSection usage={usage} modelId={modelId} />,
    );
    expect(
      container.querySelector("[data-testid=context-usage-row-day]") !== null,
    ).toBe(providerType !== "neuraldeep");
  },
);

test.each([undefined, "unavailable", "invalid"])(
  "a plan without quotas has an explicit unavailable state (%s)",
  (error) => {
    const usage = snapshot({ windows: [], error });
    expect(summarizeUsage(usage, modelId).kind).toBe("unavailable");
    expect(modelUnlimited(usage, modelId)).toBe(false);
    const { container } = render(
      <>
        <UsageSection usage={usage} modelId={modelId} />
        <UsageBanner usage={usage} modelId={modelId} />
      </>,
    );
    expect(container.querySelector(".context-usage-title")?.textContent).toBe(
      "Codex · work · Plus",
    );
    const note = container.querySelector(".context-usage-note");
    expect(note?.textContent).toBe("Quota data unavailable");
    expect(note?.classList.contains("context-usage-note--warn")).toBe(!!error);
    expect(note?.classList.contains("context-usage-note--error")).toBe(false);
    expect(container.querySelector(".context-usage-track")).toBeNull();
    expect(container.querySelector(".context-usage-pct")).toBeNull();
    expect(container.querySelector(".usage-banner")).toBeNull();
  },
);

test.each(["unavailable", "invalid"])(
  "an initial %s error without plan is visible, not a fake quota",
  (error) => {
    const usage = snapshot({ windows: undefined, plan: undefined, error });
    const { container } = render(
      <UsageSection usage={usage} modelId={modelId} />,
    );
    expect(container.querySelector(".context-usage-note")?.textContent).toBe(
      "Quota data unavailable",
    );
    expect(container.querySelector(".context-usage-note--warn")).not.toBeNull();
    expect(container.querySelector(".context-usage-track")).toBeNull();
  },
);

test("cached windows survive a failed refresh as metered stale data", () => {
  const usage = snapshot({ stale: true, error: "unavailable" });
  expect(summarizeUsage(usage, modelId)).toMatchObject({
    kind: "metered",
    stale: true,
  });
  const { container } = render(
    <UsageSection usage={usage} modelId={modelId} />,
  );
  expect(container.querySelector(".context-usage-note")?.textContent).toContain(
    "latest read failed",
  );
  expect(container.querySelector(".context-usage-pct")?.textContent).toBe(
    "85%",
  );
});

test.each([
  snapshot({ provider: "other" }),
  snapshot({ unsupported: true }),
  snapshot({ provider: "other", windows: [], error: "invalid" }),
  snapshot({ unsupported: true, windows: [], plan: "plus" }),
])("both surfaces stay scoped to a supported selected provider", (usage) => {
  const { container } = render(
    <>
      <UsageSection usage={usage} modelId={modelId} />
      <UsageBanner usage={usage} modelId={modelId} />
    </>,
  );
  expect(container.textContent).toBe("");
});

test("authorization failure keeps the rejected-key hint rather than the unavailable note", () => {
  const { container } = render(
    <UsageSection
      usage={snapshot({ error: "unauthorized" })}
      modelId={modelId}
    />,
  );
  expect(container.querySelector(".context-usage-note")?.textContent).toContain(
    "rejected",
  );
  expect(container.querySelector(".context-usage-track")).toBeNull();
});

test("only explicit unlimited flags or lists grant unlimited status", () => {
  expect(
    summarizeUsage(snapshot({ windows: [], unlimited: true }), modelId).kind,
  ).toBe("unlimited");
  expect(
    summarizeUsage(
      snapshot({ windows: [], unlimitedModels: ["model"] }),
      modelId,
    ).kind,
  ).toBe("unlimited");
  expect(modelUnlimited(snapshot({ windows: [] }), modelId)).toBe(false);
});

test("plain labels translate independently of IDs while scoped labels remain intact", () => {
  setLocale("ru");
  const usage = snapshot({
    windows: [
      { id: "custom-week", label: "week", usedPercent: 10 },
      { id: "custom-day", label: "day", usedPercent: 0 },
      { id: "week", label: "Fast model · week", usedPercent: 85 },
    ],
  });
  const { container } = render(
    <>
      <UsageSection usage={usage} modelId={modelId} />
      <UsageBanner usage={usage} modelId={modelId} />
    </>,
  );
  expect(
    [...container.querySelectorAll(".context-usage-label")].map(
      (el) => el.textContent,
    ),
  ).toEqual(["неделя", "день", "Fast model · week"]);
  expect(container.querySelector(".usage-banner-text")?.textContent).toContain(
    "Codex (Fast model · week)",
  );
  expect(usageWindowLabelKey({ id: "week" })).toBe("usage.window.week");
});

test.each([
  ["en", "Quota data unavailable"],
  ["ru", "Данные о квотах недоступны"],
])("unavailable copy is localized in %s", (locale, text) => {
  setLocale(locale);
  const { container } = render(
    <UsageSection usage={snapshot({ windows: [] })} modelId={modelId} />,
  );
  expect(container.querySelector(".context-usage-note")?.textContent).toBe(
    text,
  );
});

test("both settings dictionaries name every supported usage source", () => {
  for (const dictionary of [messagesEn, messagesRu]) {
    const description =
      dictionary["settings.schema.providers.usage_limits_panel.desc"];
    for (const brand of ["NeuralDeep", "Codex", "Devin"])
      expect(description).toContain(brand);
  }
});
