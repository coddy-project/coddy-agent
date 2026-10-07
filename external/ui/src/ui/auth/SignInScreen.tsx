import { useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { LIGHT_THEMES } from "../theme/themeCookie";
import { readAppliedUiTheme } from "../theme/uiTheme";
import { signIn, snapshotAuth } from "./authState";
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
  // The server took the password, but the session it set did not come back.
  const [notKept, setNotKept] = useState(false);
  // What happened to the Mini App's own sign-in, when the page is one.
  const auth = snapshotAuth();
  const telegramNote = auth.telegramRefused
    ? t("auth.signIn.telegramNotAdmin")
    : auth.telegramProblem === "retry"
      ? t("auth.signIn.telegramRetry")
      : "";
  const telegramNotKept = auth.telegramProblem === "not_kept";

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setNotKept(false);
    const res = await signIn(user, password);
    if (res.ok) {
      // signIn has asked the server again. A browser that does not send the
      // cookie back in this frame - a page embedded in another site, such as
      // Telegram Web running a Mini App in an iframe - is still signed out:
      // reloading would only bring this form back with no word of why.
      const after = snapshotAuth();
      if (after.loginRequired && !after.authenticated) {
        setBusy(false);
        setPassword("");
        setNotKept(true);
        return;
      }
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
      <div className="auth-shell">
        <img
          className="auth-logo"
          src={wordmark}
          alt={t("auth.signIn.logoAlt")}
          width={188}
          height={56}
        />
        {(telegramNote || telegramNotKept) && !auth.loginRequired ? (
          <div className="auth-card">
            <p className="auth-error" role="alert" data-testid="telegram-note">
              {telegramNote || t("auth.signIn.notKept")}{" "}
              {telegramNotKept ? (
                <a
                  href={window.location.href}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {t("auth.signIn.openInTab")}
                </a>
              ) : null}
            </p>
          </div>
        ) : (
          <form className="auth-card" onSubmit={submit}>
            <h1 className="auth-title">{t("auth.signIn.title")}</h1>
            {telegramNote ? (
              <p
                className="auth-error"
                role="alert"
                data-testid="telegram-note"
              >
                {telegramNote}
              </p>
            ) : null}

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

            {notKept || telegramNotKept ? (
              <p className="auth-error" role="alert">
                {t("auth.signIn.notKept")}{" "}
                <a
                  href={window.location.href}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {t("auth.signIn.openInTab")}
                </a>
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
        )}
      </div>
    </div>
  );
}
