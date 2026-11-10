import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";

import { CodexAuthField } from "./CodexAuthField";
import { I18nProvider } from "../i18n/I18nProvider";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// Regression: a sign-in started before the row's status had been read left
// the one-time code and the sign-in link standing next to "Connected with
// ChatGPT" once that read landed. A connected row says it is connected and
// offers the way out, and nothing else.
test("a row that turns out to be connected drops the code it was showing", async () => {
  const status = deferred<Response>();
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method || "GET").toUpperCase();
    if (url === "/coddy/providers/codex/codex-auth" && method === "GET") {
      return status.promise;
    }
    if (
      url === "/coddy/providers/codex/codex-auth/device" &&
      method === "POST"
    ) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            login_id: "login-1",
            verification_url: "https://chatgpt.example/device",
            user_code: "VF04-4Q4FJ",
            status: "pending",
          }),
          { status: 200 },
        ),
      );
    }
    // The poll never answers: the status read below is what connects the row.
    return new Promise<Response>(() => {});
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("open", () => null);

  render(
    <I18nProvider>
      <CodexAuthField providerName="codex" texts={{ primary: "ChatGPT" }} />
    </I18nProvider>,
  );

  screen.getByTestId("codex-auth-sign-in").click();
  expect(await screen.findByText("VF04-4Q4FJ")).toBeTruthy();

  status.resolve(
    new Response(JSON.stringify({ connected: true, source: "coddy" }), {
      status: 200,
    }),
  );

  expect(await screen.findByText("Connected with ChatGPT.")).toBeTruthy();
  await waitFor(() => {
    expect(screen.queryByText("VF04-4Q4FJ")).toBeNull();
  });
  expect(screen.queryByText("Open sign-in page")).toBeNull();
  // What is left is the way out of the account.
  expect(
    screen
      .getByTestId("codex-auth-field")
      .querySelector(".codex-auth-actions .settings-btn-danger"),
  ).not.toBeNull();
  expect(screen.queryByTestId("codex-auth-sign-in")).toBeNull();
});
