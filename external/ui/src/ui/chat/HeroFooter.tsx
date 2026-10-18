import { useEffect, useState } from "react";
import { getEnv, switchGeneration } from "../env/remoteEnv";
import { serverEventsScope } from "./sharedServerEvents";

const REPO_URL = "https://github.com/coddy-project/coddy-agent";

/** A release version as the tags spell it; a build between them says more. */
const RELEASE_RE = /^\d+\.\d+\.\d+$/;

/** The notes of a release, null for a build between releases. */
export function releaseNotesUrl(version: string): string | null {
  return RELEASE_RE.test(version)
    ? `${REPO_URL}/releases/tag/${version}`
    : null;
}

// One question per server, credential and switch: the version changes only
// with the server the page talks to, and the answer is kept by the same scope
// the events stream is (the server and a fingerprint of the token) and by the
// count of switches in place, so another server, a rotated token and the same
// remote chosen again (to read everything again) all ask again. A read that
// got no version is not kept: the next footer asks again.
let versionRead: { scope: string; answer: Promise<string> } | null = null;

/** readServerVersion asks the active environment which build it runs. */
export function readServerVersion(): Promise<string> {
  const scope = `${serverEventsScope(getEnv())}#${switchGeneration()}`;
  if (versionRead?.scope !== scope) {
    const answer = fetch("/coddy/info", {
      headers: { Accept: "application/json" },
    })
      .then((res) => (res.ok ? res.json() : null))
      .then((body: { version?: unknown } | null) =>
        typeof body?.version === "string" ? body.version.trim() : "",
      )
      .catch(() => "");
    const entry = { scope, answer };
    versionRead = entry;
    void answer.then((v) => {
      if (!v && versionRead === entry) {
        versionRead = null;
      }
    });
  }
  return versionRead.answer;
}

/** resetServerVersionForTests forgets the answer, so a test can give another. */
export function resetServerVersionForTests(): void {
  versionRead = null;
}

/**
 * HeroFooter is the line under the start screen: the project on GitHub, the
 * API reference, and the version of the server the page is talking to, on the
 * right - the local one, a remote, or a node reached through a relay. A
 * release links to its notes; a build between releases is shown as it is.
 */
export function HeroFooter() {
  const [version, setVersion] = useState("");
  useEffect(() => {
    let alive = true;
    void readServerVersion().then((v) => {
      if (alive) {
        setVersion(v);
      }
    });
    return () => {
      alive = false;
    };
  }, []);
  const label = /^\d/.test(version) ? `v${version}` : version;
  return (
    <div className="hero-footer">
      <a href={REPO_URL} target="_blank" rel="noopener">
        GitHub
      </a>
      <span className="hero-footer-sep" aria-hidden>
        |
      </span>
      <a href="/docs/" target="_blank" rel="noopener">
        API docs
      </a>
      {version ? (
        <>
          <span className="hero-footer-sep" aria-hidden>
            |
          </span>
          {releaseNotesUrl(version) ? (
            <a
              href={releaseNotesUrl(version)!}
              target="_blank"
              rel="noopener"
              className="hero-footer-version"
            >
              {label}
            </a>
          ) : (
            <span className="hero-footer-version">{label}</span>
          )}
        </>
      ) : null}
    </div>
  );
}
