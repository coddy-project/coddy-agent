import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { LIGHT_THEMES } from "../theme/themeCookie";
import { readAppliedUiTheme } from "../theme/uiTheme";
import { motionIsReduced, subscribeUiSimple } from "../theme/uiSimple";
import { totpPeriodLeft } from "../settings/totpPeriod";
import { signIn, snapshotAuth, verifySignIn } from "./authState";
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
  const { t, tp } = useT();
  // The theme is stamped on the root element before React renders, and the
  // picker that could change it is behind this screen, so reading it once is
  // enough here.
  const wordmark = LIGHT_THEMES.has(readAppliedUiTheme())
    ? wordmarkLight
    : wordmarkDark;
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(false);
  const [challenge, setChallenge] = useState("");
  const [code, setCode] = useState("");
  const [backup, setBackup] = useState(false);
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
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
  const verifying = challenge !== "";

  const reducedMotion = useSyncExternalStore(
    subscribeUiSimple,
    motionIsReduced,
    () => false,
  );
  const [, repaintPeriod] = useState(0);
  useEffect(() => {
    if (!verifying || backup) return;
    const id = window.setInterval(
      () => repaintPeriod((v) => v + 1),
      reducedMotion ? 1000 : 250,
    );
    return () => window.clearInterval(id);
  }, [verifying, backup, reducedMotion]);
  const period = totpPeriodLeft(Date.now());

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault();
    if (busyRef.current) {
      return;
    }
    busyRef.current = true;
    setBusy(true);
    setError("");
    setNotKept(false);
    const res = verifying
      ? await verifySignIn(challenge, code)
      : await signIn(user, password, remember);
    if (res.ok) {
      if (res.challenge) {
        setPassword("");
        setChallenge(res.challenge);
        setCode("");
        setBackup(false);
        busyRef.current = false;
        setBusy(false);
        return;
      }
      // A successful request has asked the server again. A browser that does
      // not send the cookie back in this frame - a page embedded in another
      // site, such as Telegram Web running a Mini App in an iframe - is still
      // signed out: reloading would only bring this form back with no word of
      // why.
      const after = snapshotAuth();
      if (after.loginRequired && !after.authenticated) {
        busyRef.current = false;
        setBusy(false);
        setPassword("");
        setChallenge("");
        setCode("");
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
    busyRef.current = false;
    setBusy(false);
    setCode("");
    // A completed verify attempt consumes its challenge, successfully or not.
    // Only a request that never reached the server may be explicitly retried.
    if (!verifying || res.status !== 0) {
      setPassword("");
    }
    if (verifying && res.status !== 0) {
      setChallenge("");
    }
    if (res.status === 401) {
      setError(
        verifying ? t("auth.signIn.invalidCode") : t("auth.signIn.invalid"),
      );
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
            {verifying ? (
              <div className="auth-step">{t("auth.signIn.secondStep")}</div>
            ) : null}
            <h1 className="auth-title">
              {t(verifying ? "auth.signIn.verifyTitle" : "auth.signIn.title")}
            </h1>
            {telegramNote ? (
              <p
                className="auth-error"
                role="alert"
                data-testid="telegram-note"
              >
                {telegramNote}
              </p>
            ) : null}

            {verifying ? (
              <>
                <p className="auth-copy">
                  {t(
                    backup
                      ? "auth.signIn.backupCopy"
                      : "auth.signIn.verifyCopy",
                  )}
                </p>
                <label className="auth-field">
                  <span className="auth-label">
                    {t(backup ? "auth.signIn.backupCode" : "auth.signIn.code")}
                  </span>
                  <input
                    className="auth-input auth-code-input"
                    type="text"
                    name={backup ? "backup-code" : "one-time-code"}
                    autoComplete="one-time-code"
                    inputMode={backup ? "text" : "numeric"}
                    maxLength={backup ? 32 : 6}
                    placeholder={backup ? "8F3K-92M Q7X" : "000000"}
                    autoCapitalize={backup ? "characters" : undefined}
                    autoCorrect={backup ? "off" : undefined}
                    spellCheck={backup ? false : undefined}
                    autoFocus
                    data-testid="sign-in-code"
                    value={code}
                    onChange={(e) =>
                      setCode(
                        backup
                          ? e.target.value
                          : e.target.value.replace(/\D/g, "").slice(0, 6),
                      )
                    }
                    disabled={busy}
                  />
                </label>
                {!backup ? (
                  <div className="auth-period" data-testid="sign-in-period">
                    <span className="auth-period-label">
                      {tp("auth.signIn.period", period.seconds)}
                    </span>
                    <span className="auth-period-track" aria-hidden="true">
                      <span
                        className="auth-period-fill"
                        style={{ width: `${period.fraction * 100}%` }}
                      />
                    </span>
                  </div>
                ) : null}
              </>
            ) : (
              <>
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
                  <span className="auth-label">
                    {t("auth.signIn.password")}
                  </span>
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

                {auth.rememberAvailable !== false && (
                  <label className="auth-remember">
                    <input
                      type="checkbox"
                      checked={remember}
                      disabled={busy}
                      onChange={(event) => setRemember(event.target.checked)}
                    />
                    <span>{t("auth.signIn.remember")}</span>
                  </label>
                )}
              </>
            )}

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
              disabled={
                busy ||
                (verifying
                  ? backup
                    ? code.trim() === ""
                    : !/^\d{6}$/.test(code)
                  : user.trim() === "" || password === "")
              }
            >
              {busy
                ? t("auth.signIn.working")
                : t(
                    verifying
                      ? "auth.signIn.verifySubmit"
                      : "auth.signIn.submit",
                  )}
            </button>
            {verifying ? (
              <div className="auth-alt">
                <button
                  className="auth-text-button"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setChallenge("");
                    setCode("");
                    setBackup(false);
                    setError("");
                  }}
                >
                  {t("auth.signIn.back")}
                </button>
                <button
                  className="auth-text-button"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setBackup((value) => !value);
                    setCode("");
                    setError("");
                  }}
                >
                  {t(
                    backup
                      ? "auth.signIn.useAuthenticator"
                      : "auth.signIn.useBackup",
                  )}
                </button>
              </div>
            ) : null}
          </form>
        )}
      </div>
    </div>
  );
}
