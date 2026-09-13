import { useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { LIGHT_THEMES } from "../theme/themeCookie";
import { readAppliedUiTheme } from "../theme/uiTheme";
import { signIn } from "./authState";
// Both wordmarks are small enough that Vite inlines them into the bundle, so
// the screen paints with no second request - which matters on the one page a
// browser sees before it has any credential at all.
import wordmarkDark from "../../assets/coddy-logo-wordmark.svg";
import wordmarkLight from "../../assets/coddy-logo-wordmark-light.svg";

/**
 * SignInScreen is what an anonymous browser sees instead of the app when
 * `httpserver.login` is configured.
 *
 * It is the whole page, not a dialog over a blurred transcript: there is
 * nothing behind it to look at, and a form floating over other people's
 * conversations would suggest otherwise.
 */
export function SignInScreen(props: { onSignedIn?: () => void }) {
  const { t } = useT();
  // The theme is stamped on the root element before React renders, and the
  // picker that could change it is behind this screen, so reading it once is
  // enough here.
  const wordmark = LIGHT_THEMES.has(readAppliedUiTheme())
    ? wordmarkLight
    : wordmarkDark;
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    const res = await signIn(user, password);
    if (res.ok) {
      // A reload rather than a re-render: every list, stream and cached
      // response on the page was fetched by a browser that had no session, so
      // starting over is both simpler and more honest than patching them up.
      if (props.onSignedIn) {
        props.onSignedIn();
      } else {
        window.location.reload();
      }
      return;
    }
    setBusy(false);
    setPassword("");
    if (res.status === 401) {
      setError(t("auth.signIn.invalid"));
    } else if (res.status === 0) {
      setError(t("auth.signIn.unreachable"));
    } else if (res.status === 503) {
      setError(t("auth.signIn.noAccount"));
    } else if (res.status === 403) {
      setError(t("auth.signIn.crossSite"));
    } else {
      setError(t("auth.signIn.failed", { status: String(res.status) }));
    }
  };

  return (
    <div className="auth-screen" data-testid="sign-in-screen">
      <form className="auth-card" onSubmit={submit}>
        <img
          className="auth-logo"
          src={wordmark}
          alt={t("auth.signIn.logoAlt")}
          width={188}
          height={56}
        />
        <h1 className="auth-title">{t("auth.signIn.title")}</h1>

        <label className="auth-field">
          <span className="auth-label">{t("auth.signIn.user")}</span>
          <input
            className="auth-input"
            type="text"
            name="username"
            autoComplete="username"
            autoFocus
            value={user}
            onChange={(e) => setUser(e.target.value)}
            disabled={busy}
          />
        </label>

        <label className="auth-field">
          <span className="auth-label">{t("auth.signIn.password")}</span>
          <input
            className="auth-input"
            type="password"
            name="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={busy}
          />
        </label>

        {error ? (
          <p className="auth-error" role="alert">
            {error}
          </p>
        ) : null}

        <button
          className="auth-submit"
          type="submit"
          disabled={busy || user.trim() === "" || password === ""}
        >
          {busy ? t("auth.signIn.working") : t("auth.signIn.submit")}
        </button>
      </form>
    </div>
  );
}
