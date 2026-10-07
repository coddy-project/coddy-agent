import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";

let respond: (url: string, init?: RequestInit) => Response = () =>
  ({ ok: true, status: 200, json: async () => ({}) }) as Response;
const seen: { url: string; body?: string }[] = [];
let envMode: "local" | "remote" = "local";
// hang makes the server answer never come, which is the state the gate has to
// render something for.
let hang = false;

vi.mock("../env/remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../env/remoteEnv")>();
  return {
    ...actual,
    getEnv: () =>
      envMode === "local"
        ? { mode: "local" }
        : { mode: "remote", baseUrl: "http://box:12345", token: "t" },
    onLocalApiUnauthorized: () => () => {},
    localFetch: (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: String(input), body: init?.body as string });
      if (hang) {
        return new Promise<Response>(() => {});
      }
      return Promise.resolve(respond(String(input), init));
    },
  };
});

const { AuthGate } = await import("./AuthGate");
const { resetAuthStateForTests } = await import("./authState");

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}

function renderGate() {
  return render(
    <I18nProvider>
      <AuthGate>
        <div data-testid="the-app">the app</div>
      </AuthGate>
    </I18nProvider>,
  );
}

beforeEach(() => {
  initLocale("en");
  document.documentElement.dataset.theme = "dark";
  resetAuthStateForTests();
  seen.length = 0;
  envMode = "local";
  hang = false;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("AuthGate", () => {
  it("renders the app when the server asks for no sign-in", async () => {
    respond = () => jsonResponse(200, { login_required: false });
    renderGate();
    expect(await screen.findByTestId("the-app")).toBeInTheDocument();
  });

  it("renders the sign-in screen instead of the app for an anonymous browser", async () => {
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    renderGate();
    expect(await screen.findByTestId("sign-in-screen")).toBeInTheDocument();
    expect(screen.queryByTestId("the-app")).not.toBeInTheDocument();
  });

  it("renders the app once the browser is signed in", async () => {
    respond = () =>
      jsonResponse(200, {
        login_required: true,
        authenticated: true,
        user: "operator",
      });
    renderGate();
    expect(await screen.findByTestId("the-app")).toBeInTheDocument();
  });

  it("shows nothing of the app before the server has answered", async () => {
    // Rendering the transcript and then yanking it away to a sign-in form
    // reads as a glitch, so the gate waits out the one round trip.
    hang = true;
    const { container } = renderGate();
    expect(container.querySelector(".auth-boot")).not.toBeNull();
    expect(screen.queryByTestId("the-app")).not.toBeInTheDocument();
    expect(screen.queryByTestId("sign-in-screen")).not.toBeInTheDocument();
  });

  it("leaves a remote environment alone, which authenticates with a token", async () => {
    // Cookies from this origin never travel to a remote, so gating it here
    // would only hide an app that works.
    envMode = "remote";
    renderGate();
    expect(await screen.findByTestId("the-app")).toBeInTheDocument();
    expect(seen).toHaveLength(0);
  });

  it("treats a server that cannot answer as one that wants no sign-in", async () => {
    respond = () => {
      throw new Error("connection refused");
    };
    renderGate();
    expect(await screen.findByTestId("the-app")).toBeInTheDocument();
  });
});

describe("SignInScreen", () => {
  it("sends the credentials and opens the app on success", async () => {
    const reload = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...window.location, reload },
    });
    let signedIn = false;
    respond = (url) => {
      if (url === "/coddy/auth/login") {
        signedIn = true;
        return jsonResponse(200, { ok: true, user: "operator" });
      }
      return jsonResponse(200, {
        login_required: true,
        authenticated: signedIn,
      });
    };
    renderGate();
    await screen.findByTestId("sign-in-screen");

    fireEvent.change(screen.getByLabelText("User"), {
      target: { value: "operator" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "correct-horse" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => expect(reload).toHaveBeenCalled());
    const post = seen.find((s) => s.url === "/coddy/auth/login");
    expect(post?.body).toBe(
      JSON.stringify({ user: "operator", password: "correct-horse" }),
    );
  });

  // A page embedded in another site (Telegram Web runs a Mini App in an
  // iframe) gets the cookie of a sign-in the server accepted, and the browser
  // does not send it back. A reload would only bring the form back with no
  // word of why; the screen says what happened and offers a tab of its own.
  it("says the browser did not keep the sign-in when the session does not come back", async () => {
    const reload = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...window.location,
        reload,
        href: "https://coddy.example.com/#/s/sess_1",
      },
    });
    respond = (url) => {
      if (url === "/coddy/auth/login") {
        return jsonResponse(200, { ok: true, user: "operator" });
      }
      return jsonResponse(200, { login_required: true, authenticated: false });
    };
    renderGate();
    await screen.findByTestId("sign-in-screen");
    fireEvent.change(screen.getByLabelText("User"), {
      target: { value: "operator" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "correct-horse" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("did not keep it");
    const link = screen.getByRole("link", {
      name: "Open Coddy in a tab of its own",
    });
    expect(link).toHaveAttribute(
      "href",
      "https://coddy.example.com/#/s/sess_1",
    );
    expect(link).toHaveAttribute("target", "_blank");
    expect(reload).not.toHaveBeenCalled();
  });

  it("says the credentials were wrong and clears the password", async () => {
    respond = (url) => {
      if (url === "/coddy/auth/login") {
        return jsonResponse(401, { error: "invalid credentials" });
      }
      return jsonResponse(200, { login_required: true, authenticated: false });
    };
    renderGate();
    await screen.findByTestId("sign-in-screen");

    fireEvent.change(screen.getByLabelText("User"), {
      target: { value: "operator" },
    });
    const password = screen.getByLabelText("Password");
    fireEvent.change(password, { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Wrong user or password.",
    );
    expect(password).toHaveValue("");
    // ...and the app is still not rendered.
    expect(screen.queryByTestId("the-app")).not.toBeInTheDocument();
  });

  it("names the misconfiguration when the form has no account behind it", async () => {
    respond = (url) => {
      if (url === "/coddy/auth/login") {
        return jsonResponse(503, { error: "no account" });
      }
      return jsonResponse(200, { login_required: true, authenticated: false });
    };
    renderGate();
    await screen.findByTestId("sign-in-screen");
    fireEvent.change(screen.getByLabelText("User"), {
      target: { value: "operator" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "whatever" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("set-password");
  });

  it("shows the wordmark, which is where the product name now lives", async () => {
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    renderGate();
    await screen.findByTestId("sign-in-screen");
    const logo = screen.getByRole("img", { name: "Coddy agent" });
    // Vite inlines both wordmarks, so the one screen a browser sees before it
    // has any credential paints without a second request.
    expect(logo.getAttribute("src") ?? "").toMatch(/^data:image\/svg\+xml/);
  });

  it("takes the light wordmark on the light theme", async () => {
    document.documentElement.dataset.theme = "light";
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    renderGate();
    await screen.findByTestId("sign-in-screen");
    const light = screen.getByRole("img", { name: "Coddy agent" });
    const lightSrc = light.getAttribute("src") ?? "";
    cleanup();

    document.documentElement.dataset.theme = "dark";
    resetAuthStateForTests();
    renderGate();
    await screen.findByTestId("sign-in-screen");
    const darkSrc =
      screen.getByRole("img", { name: "Coddy agent" }).getAttribute("src") ??
      "";
    expect(lightSrc).not.toBe(darkSrc);
  });

  it("will not submit an empty form", async () => {
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    renderGate();
    await screen.findByTestId("sign-in-screen");
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
  });
});

describe("SignInScreen after a Telegram Mini App sign-in", () => {
  afterEach(() => {
    cleanup();
    resetAuthStateForTests();
  });

  async function renderWith(state: Partial<import("./authState").AuthState>) {
    const { setAuthState, snapshotAuth } = await import("./authState");
    const { SignInScreen } = await import("./SignInScreen");
    setAuthState({ ...snapshotAuth(), loaded: true, ...state });
    initLocale("en");
    render(
      <I18nProvider>
        <SignInScreen />
      </I18nProvider>,
    );
  }

  it("asks to reopen the Mini App above the password form", async () => {
    await renderWith({ loginRequired: true, telegramProblem: "retry" });
    expect(screen.getByTestId("telegram-note").textContent).toMatch(
      /open it again/,
    );
    expect(document.querySelector("form")).not.toBeNull();
  });

  it("offers a tab of its own instead of a useless form when the session was not kept", async () => {
    await renderWith({ loginRequired: false, telegramProblem: "not_kept" });
    expect(document.querySelector("form")).toBeNull();
    expect(screen.getByRole("link").getAttribute("target")).toBe("_blank");
  });
});
