import { useEffect, useRef, useState } from "react";
import { landmarkAnchor } from "../settings/settingsSections";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { useT } from "../i18n/I18nProvider";
import { FieldLabel, LegendWithHint } from "../settings/FieldHint";
import { SwitchField } from "../settings/SwitchField";
import { registerStatisticsSettings } from "./statisticsSettingsStore";
import {
  browserTimezone,
  statisticsRequest,
  type StatisticsData,
  type StatisticsSettings,
} from "./api";

type Rate = { name: string; input: string; output: string; cached: string };

/** Every IANA zone the browser knows, else the few a server is usually set to. */
function zoneOptions(): string[] {
  const supported = (
    Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
  ).supportedValuesOf;
  const all = supported ? supported("timeZone") : [];
  if (all.length) return all;
  return [
    ...new Set([
      browserTimezone(),
      "UTC",
      "Europe/Moscow",
      "Europe/London",
      "America/New_York",
      "Asia/Tokyo",
    ]),
  ].sort();
}

function ratesOf(settings: StatisticsSettings): Rate[] {
  return Object.entries(settings.prices).map(([name, p]) => ({
    name,
    input: String(p.input),
    output: String(p.output),
    // A tariff saved over the API without a cache rate reads the input rate;
    // the form shows that rate rather than an empty field.
    cached: String(p.cached ?? p.input),
  }));
}

export default function StatisticsSettings() {
  const { t } = useT();
  const [settings, setSettings] = useState<StatisticsSettings | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [timezone, setTimezone] = useState("");
  const [rates, setRates] = useState<Rate[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const zones = useRef<string[]>([]);
  if (!zones.current.length) zones.current = zoneOptions();

  const load = async () => {
    const data = await statisticsRequest<StatisticsData>(
      `/coddy/statistics?timezone=${encodeURIComponent(browserTimezone())}`,
    );
    const next = data.settings!;
    setSettings(next);
    setEnabled(!next.disabled);
    setTimezone(next.timezone);
    setRates(ratesOf(next));
    setModels(data.allTime.models.map((m) => m.name));
  };
  useEffect(() => {
    void load().catch((e) => setError(String(e)));
  }, []);

  // What the drawer's Save writes, and whether it has anything to write.
  const save = async () => {
    const prices: StatisticsSettings["prices"] = {};
    for (const r of rates) {
      if (
        !r.name.trim() ||
        Object.hasOwn(prices, r.name.trim()) ||
        [r.input, r.output, r.cached].some(
          (v) =>
            v.trim() === "" || !Number.isFinite(Number(v)) || Number(v) < 0,
        )
      )
        throw new Error(t("stats.prices.invalid"));
      prices[r.name.trim()] = {
        input: Number(r.input),
        output: Number(r.output),
        cached: Number(r.cached),
      };
    }
    const next = await statisticsRequest<StatisticsSettings>(
      "/coddy/statistics/settings",
      { method: "PUT", body: JSON.stringify({ enabled, timezone, prices }) },
    );
    setSettings(next);
    setEnabled(!next.disabled);
    setRates(ratesOf(next));
  };
  const dirty =
    settings !== null &&
    (enabled === (settings.disabled ?? false) ||
      timezone !== settings.timezone ||
      JSON.stringify(rates) !== JSON.stringify(ratesOf(settings)));
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    registerStatisticsSettings({
      dirty,
      save: () => saveRef.current(),
    });
    return () => registerStatisticsSettings(null);
  }, [dirty]);

  const reset = async () => {
    setBusy(true);
    setError("");
    try {
      await statisticsRequest("/coddy/statistics?keep=none", {
        method: "DELETE",
      });
      setConfirm(false);
      await load();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const changeRate = (index: number, field: keyof Rate, value: string) =>
    setRates((old) =>
      old.map((r, i) => (i === index ? { ...r, [field]: value } : r)),
    );

  return (
    <div
      className="statistics-settings"
      data-testid="statistics-settings"
      data-settings-pending={!settings && !error ? true : undefined}
    >
      {error && (
        <p className="statistics-error" role="alert">
          {error}
        </p>
      )}
      {settings && (
        <>
          {/* The switch opens the form, outside every fieldset: it decides
              whether there is anything to configure at all. */}
          <div data-settings-anchor={landmarkAnchor("statistics", "enable")}>
            <SwitchField
              checked={enabled}
              onChange={setEnabled}
              label={t("stats.enable")}
              description={t("stats.enable.hint")}
              dataTestId="statistics-enabled"
            />
          </div>
          <fieldset
            className="settings-fieldset"
            disabled={!enabled}
            data-settings-anchor={landmarkAnchor("statistics", "timezone")}
          >
            <LegendWithHint
              label={t("stats.counters")}
              description={t("stats.counters.hint")}
            />
            <div className="settings-form-group-body">
              <label className="settings-field">
                <FieldLabel
                  label={t("stats.timezone")}
                  description={t("stats.timezone.hint")}
                />
                <span className="appearance-language-select-wrap">
                  <select
                    className="appearance-language-select"
                    data-testid="statistics-timezone"
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                  >
                    <option value="">{t("stats.timezone.browser")}</option>
                    {zones.current.map((zone) => (
                      <option key={zone} value={zone}>
                        {zone}
                      </option>
                    ))}
                  </select>
                </span>
              </label>
              <div className="settings-field statistics-reset-field">
                <FieldLabel
                  label={t("stats.clear.heading")}
                  description={t("stats.clear.hint")}
                />
                <button
                  className="settings-btn settings-btn-danger statistics-danger"
                  data-testid="statistics-reset"
                  disabled={busy}
                  onClick={() => setConfirm(true)}
                >
                  {t("stats.clear.action")}
                </button>
              </div>
            </div>
          </fieldset>

          <fieldset
            className="settings-fieldset"
            disabled={!enabled}
            data-settings-anchor={landmarkAnchor("statistics", "prices")}
          >
            <LegendWithHint
              label={t("stats.prices")}
              description={t("stats.prices.hint")}
            />
            <div className="settings-form-group-body">
              {rates.map((r, i) => (
                <fieldset
                  className="settings-fieldset settings-array-entry"
                  key={i}
                >
                  <label className="settings-field">
                    <FieldLabel label={t("stats.model")} />
                    <input
                      className="settings-input"
                      list="statistics-price-models"
                      value={r.name}
                      onChange={(e) => changeRate(i, "name", e.target.value)}
                    />
                  </label>
                  {(["input", "output", "cached"] as const).map((field) => (
                    <label className="settings-field" key={field}>
                      <FieldLabel label={t(`stats.${field}`)} />
                      <input
                        className="settings-input"
                        type="number"
                        min="0"
                        step="any"
                        value={r[field]}
                        onChange={(e) => changeRate(i, field, e.target.value)}
                      />
                    </label>
                  ))}
                  <button
                    className="settings-btn settings-btn-icon statistics-rate-remove"
                    aria-label={t("stats.prices.remove")}
                    onClick={() =>
                      setRates((old) => old.filter((_, j) => i !== j))
                    }
                  >
                    <svg
                      width="16"
                      height="16"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      aria-hidden="true"
                    >
                      <path d="m6 6 12 12M18 6 6 18" />
                    </svg>
                  </button>
                </fieldset>
              ))}
              <datalist id="statistics-price-models">
                {models.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
            </div>
            {/* Outside the column of fields, so it takes the width of its
                own text, the way a list of providers adds a row. */}
            <button
              className="settings-btn"
              onClick={() =>
                setRates((old) => [
                  ...old,
                  { name: "", input: "", output: "", cached: "" },
                ])
              }
            >
              {t("stats.prices.add")}
            </button>
          </fieldset>
        </>
      )}
      <ConfirmDialog
        open={confirm}
        title={t("stats.clear.title")}
        message={t("stats.clear.message")}
        confirmLabel={t("stats.clear.action")}
        variant="danger"
        ariaLabel={t("stats.clear.title")}
        onConfirm={() => void reset()}
        onCancel={() => setConfirm(false)}
        confirming={busy}
      />
    </div>
  );
}
