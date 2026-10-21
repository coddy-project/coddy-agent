import { useContext, useEffect, useId, useState, type ReactNode } from "react";
import { PaneHidden } from "../components/PanePortal";
import { readServerVersion, releaseNotesUrl } from "../chat/HeroFooter";
import { copyText } from "../clipboard";
import { useT } from "../i18n/I18nProvider";
import { appNavHrefDocs } from "../scheduler/hashRoute";
import { RestartNotice } from "./RestartNotice";
import {
  landmarkAnchor,
  SETTINGS_TEXTS,
  type DrawnText,
} from "./settingsSections";
import { Sparkline } from "./Sparkline";
import {
  ago,
  bytes,
  chartsOf,
  count,
  kindName,
  percent,
  relayLines,
  seconds,
  span,
  summarize,
  tasksText,
  word,
  type BlockId,
} from "./systemStatus";
import {
  openSystemStatus,
  pendingAddresses,
  readSystemStatus,
  useSystemStatus,
} from "./systemStatusApi";

/** How often the open tab reads the status again. */
const POLL_MS = 15_000;

function Row(props: { label: string; children: ReactNode }) {
  return (
    <>
      <dt>{props.label}</dt>
      <dd>{props.children}</dd>
    </>
  );
}

/**
 * A block the settings search finds by its heading: the box carries the
 * landmark anchor and the heading is the first thing in it. It stands at module
 * level on purpose: a component type made inside the section would be a new
 * one on every read and remount the block, taking the focus from a heading and
 * from the copy button.
 */
function Block(props: {
  base: string;
  id: "installation" | "subsystems" | "disk" | "activity";
  text: DrawnText;
  /** A server that answered nothing has nothing to say here: `na` stands in. */
  empty: boolean;
  na: ReactNode;
  children?: ReactNode;
}) {
  const { t } = useT();
  return (
    <section
      className="system-block"
      data-testid={`system-${props.id}`}
      data-settings-anchor={landmarkAnchor("status", props.id)}
      aria-labelledby={`${props.base}-${props.id}`}
    >
      <h3
        id={`${props.base}-${props.id}`}
        className="appearance-section-label system-block-title"
        tabIndex={-1}
      >
        {t(props.text.label!)}
      </h3>
      {props.empty ? props.na : props.children}
    </section>
  );
}

function CopyButton(props: { text: string }) {
  const { t } = useT();
  const [done, setDone] = useState(false);
  const copy = () =>
    void copyText(props.text).then((ok) => {
      if (ok) {
        setDone(true);
        window.setTimeout(() => setDone(false), 2000);
      }
    });
  return (
    <button type="button" className="settings-btn system-copy" onClick={copy}>
      {done ? t("system.copied") : t("system.copy")}
    </button>
  );
}

/** "Updated 12 sec. ago", kept current by a clock of its own. */
function Updated(props: { at: number }) {
  const { t } = useT();
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") {
        setNow(Date.now());
      }
    }, 1000);
    return () => window.clearInterval(id);
  }, []);
  return (
    <span data-testid="system-updated">
      {t("system.updated", { ago: ago(now - props.at) })}
    </span>
  );
}

/**
 * Settings -> System: what this installation is, how it runs and what needs
 * attention, read from GET /coddy/status. It edits nothing. The four blocks
 * stand on the first frame, whatever the server answers; the charts and the
 * providers come only with a document that has `metrics`. The summary is the
 * only live region.
 */
export default function SystemSection() {
  const { t, locale } = useT();
  const state = useSystemStatus((s) => s);
  const hidden = useContext(PaneHidden);
  const base = useId();
  const [version, setVersion] = useState("");

  // On open, then every 15 s while the tab is shown and the page is visible.
  useEffect(() => {
    if (hidden) {
      return undefined;
    }
    void openSystemStatus();
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") {
        void readSystemStatus();
      }
    }, POLL_MS);
    return () => window.clearInterval(id);
  }, [hidden]);

  // A server that does not report its status is named by its version.
  const missing = state.issue?.kind === "notFound";
  useEffect(() => {
    if (missing) {
      void readServerVersion().then(setVersion);
    }
  }, [missing]);

  const doc = state.doc ?? state.stale?.doc ?? null;
  const old = state.doc === null && state.stale !== null;
  const at = state.doc !== null ? state.at : (state.stale?.at ?? 0);
  const summary = summarize(state, version);
  const focus = (block: BlockId) =>
    document.getElementById(`${base}-${block}`)?.focus();
  // A server that answered nothing has nothing to say in any block.
  const empty = doc === null && state.at > 0;
  const na = <p className="system-muted">{t("system.na")}</p>;

  const build = doc?.build;
  const lowDisk = doc?.attention?.some((a) => a.code === "disk_low") === true;
  const disk = doc?.disk;
  const used =
    disk && disk.sizeBytes > 0
      ? Math.min(
          100,
          Math.max(0, (1 - disk.availableBytes / disk.sizeBytes) * 100),
        )
      : 0;
  const freeText = disk
    ? t("system.disk.free", {
        free: bytes(disk.availableBytes),
        size: bytes(disk.sizeBytes),
      })
    : "";
  const notes = build ? releaseNotesUrl(build.version) : null;
  const metrics = doc?.metrics;
  const relays = doc ? relayLines(doc) : [];

  return (
    <div
      className={`system-section${old ? " is-stale" : ""}`}
      data-testid="system-section"
    >
      <div
        className="system-summary"
        data-testid="system-summary"
        data-state={summary.state}
        aria-live="polite"
      >
        {summary.lines.map((line, i) => (
          <p key={i} className={line.quiet ? "system-muted" : undefined}>
            {line.text}{" "}
            {line.items.map((item, j) => (
              <span key={j}>
                {j > 0 ? " · " : null}
                <button
                  type="button"
                  className="system-link"
                  onClick={() => focus(item.block)}
                >
                  {item.text}
                </button>
              </span>
            ))}
          </p>
        ))}
      </div>
      {state.doc && (state.doc.restartRequired || state.doc.restartRefused) ? (
        <RestartNotice
          refused={state.doc.restartRefused ?? ""}
          address={pendingAddresses(state.doc)}
        />
      ) : null}
      <div className="system-blocks">
        {metrics ? (
          <section className="system-block system-charts">
            <h3 id={`${base}-last`} className="system-h" tabIndex={-1}>
              {t("system.last")}
            </h3>
            <p className="system-muted">{t("system.last.note")}</p>
            <div className="system-chart-grid">
              {chartsOf(metrics.series).map((chart) => (
                <div key={chart.id} className="system-chart">
                  <b>{chart.title}</b>
                  <div className="system-plot">
                    {chart.lines.map((line, i) => (
                      <Sparkline
                        key={i}
                        values={line.values}
                        max={chart.max}
                        tone={line.tone}
                        {...(line.step ? { step: true } : {})}
                        label={chart.title}
                        latest={chart.value}
                        hidden={i > 0}
                      />
                    ))}
                  </div>
                  <span className="system-chart-value">{chart.value}</span>
                  <div className="system-chart-legend">
                    {chart.legend.map((l) => (
                      <span key={l.text} className={`system-key--${l.tone}`}>
                        {l.text}
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </section>
        ) : null}

        <Block
          base={base}
          id="installation"
          text={SETTINGS_TEXTS.systemInstallation}
          empty={empty}
          na={na}
        >
          {build && doc ? (
            <dl className="system-kv">
              <Row label={t("system.version")}>
                <span className="system-code">
                  {notes ? (
                    <a href={notes} target="_blank" rel="noopener">
                      {build.version}
                    </a>
                  ) : (
                    build.version
                  )}
                </span>
                {build.revision ? (
                  <>
                    {" "}
                    <span className="system-muted">
                      ({build.revision.slice(0, 8)})
                    </span>
                  </>
                ) : null}
              </Row>
              {build.tags?.length ? (
                <Row label={t("system.tags")}>{build.tags.join(" · ")}</Row>
              ) : null}
              <Row label={t("system.platform")}>
                {`${build.os}/${build.arch} · ${build.goVersion}`}
              </Row>
              {Date.parse(doc.generatedAt) >
              Date.parse(doc.process.startedAt) ? (
                <Row label={t("system.running")}>
                  {span(
                    (Date.parse(doc.generatedAt) -
                      Date.parse(doc.process.startedAt)) /
                      1000,
                  )}
                </Row>
              ) : null}
              <Row label={t("system.startedBy")}>
                {word("system.startedBy", doc.process.startedBy)}
              </Row>
              {doc.process.configFile ? (
                <Row label={t("system.config")}>
                  <code className="system-code">{doc.process.configFile}</code>{" "}
                  <CopyButton text={doc.process.configFile} />
                </Row>
              ) : null}
            </dl>
          ) : null}
        </Block>

        <Block
          base={base}
          id="subsystems"
          text={SETTINGS_TEXTS.systemSubsystems}
          empty={empty}
          na={na}
        >
          {doc ? (
            <>
              {doc.subsystems.length > 0 ? (
                <dl className="system-kv">
                  {doc.subsystems.map((sub) => (
                    <Row key={sub.kind} label={kindName(sub.kind)}>
                      <span className="system-pill" data-state={sub.state}>
                        {word("system.state", sub.state)}
                      </span>
                      {sub.address ? (
                        <span className="system-code">{sub.address}</span>
                      ) : null}
                      {sub.pendingAddress ? (
                        <span className="system-warn">
                          {t("system.afterRestart", {
                            address: sub.pendingAddress,
                          })}
                        </span>
                      ) : null}
                      {sub.kind === "metrics" && sub.credential ? (
                        <span className="system-muted">
                          {`/metrics · ${t(sub.credential === "token" ? "system.token.set" : "system.token.none")}`}
                        </span>
                      ) : null}
                    </Row>
                  ))}
                </dl>
              ) : relays.length === 0 ? (
                <p className="system-muted">{t("system.none")}</p>
              ) : null}
              {relays.map((line, i) => (
                <p key={i} className="system-warn system-relay">
                  {line}
                </p>
              ))}
            </>
          ) : null}
        </Block>

        <Block
          base={base}
          id="disk"
          text={SETTINGS_TEXTS.systemDisk}
          empty={empty}
          na={na}
        >
          {doc ? (
            disk && !doc.unavailable?.includes("disk") ? (
              <div className="system-disk">
                <span>{t("system.disk.sessions")}</span>
                <div
                  className="system-meter"
                  role="meter"
                  aria-label={t("system.disk.used")}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={Math.round(used * 10) / 10}
                  aria-valuetext={freeText}
                  data-tone={lowDisk ? "warn" : "ok"}
                >
                  <i style={{ width: `${used}%` }} />
                </div>
                <span>{freeText}</span>
              </div>
            ) : (
              na
            )
          ) : null}
        </Block>

        <Block
          base={base}
          id="activity"
          text={SETTINGS_TEXTS.systemActivity}
          empty={empty}
          na={na}
        >
          {doc ? (
            <dl className="system-kv">
              <Row label={t("system.turns")}>{count(doc.turns.running)}</Row>
              {doc.turns.oldestSeconds !== undefined ? (
                <Row label={t("system.oldest")}>
                  {span(doc.turns.oldestSeconds)}
                </Row>
              ) : null}
              <Row label={t("system.tasks")}>
                {tasksText(doc.tasks) || t("system.none")}
              </Row>
              <Row label={t("system.prompts.permission")}>
                {count(doc.prompts?.permission ?? 0)}
              </Row>
              <Row label={t("system.prompts.question")}>
                {count(doc.prompts?.question ?? 0)}
              </Row>
            </dl>
          ) : null}
        </Block>

        {metrics ? (
          <section className="system-block">
            <h3 id={`${base}-providers`} className="system-h" tabIndex={-1}>
              {t("system.providers")}
            </h3>
            <div className="system-table-wrap">
              <table className="system-table">
                <thead>
                  <tr>
                    <th>{t("system.col.provider")}</th>
                    <th>{t("system.col.attempts")}</th>
                    <th>{t("system.col.errors")}</th>
                    <th>{t("system.col.time")}</th>
                    <th>{t("system.col.last")}</th>
                  </tr>
                </thead>
                <tbody>
                  {metrics.providers.map((p) => (
                    <tr key={p.name}>
                      <td>{p.name}</td>
                      <td>{count(p.attempts)}</td>
                      <td>
                        {p.attempts > 0
                          ? percent(p.failures / p.attempts)
                          : "—"}
                      </td>
                      <td>
                        {p.meanSeconds !== undefined
                          ? seconds(p.meanSeconds)
                          : "—"}
                      </td>
                      <td>
                        {p.lastFailureAt
                          ? ago(Date.now() - Date.parse(p.lastFailureAt))
                          : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ) : null}
      </div>
      <footer className="system-foot" lang={locale}>
        {at > 0 ? <Updated at={at} /> : <span />}
        <a href={appNavHrefDocs("operate/monitoring", "the-system-page")}>
          {t("system.docs")}
        </a>
      </footer>
    </div>
  );
}
