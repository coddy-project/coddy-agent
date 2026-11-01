import { useEffect, useState } from "react";
import { landmarkAnchor } from "../settings/settingsSections";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { useT } from "../i18n/I18nProvider";
import {
  browserTimezone,
  statisticsRequest,
  type StatisticsData,
  type StatisticsSettings,
} from "./api";

type Rate = { name: string; input: string; output: string; cached: string };

export default function StatisticsSettings() {
  const { t } = useT();
  const [settings, setSettings] = useState<StatisticsSettings | null>(null);
  const [timezone, setTimezone] = useState("");
  const [rates, setRates] = useState<Rate[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [keep, setKeep] = useState("month");
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const load = async () => {
    const data = await statisticsRequest<StatisticsData>(
      `/coddy/statistics?timezone=${encodeURIComponent(browserTimezone())}`,
    );
    const next = data.settings!;
    setSettings(next);
    setTimezone(next.timezone);
    setRates(
      Object.entries(next.prices).map(([name, p]) => ({
        name,
        input: String(p.input),
        output: String(p.output),
        cached: String(p.cached),
      })),
    );
    setModels(data.allTime.models.map((m) => m.name));
  };
  useEffect(() => {
    void load().catch((e) => setError(String(e)));
  }, []);
  const save = async () => {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
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
        { method: "PUT", body: JSON.stringify({ timezone, prices }) },
      );
      setSettings(next);
      setSaved(true);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    setBusy(true);
    setError("");
    try {
      await statisticsRequest(`/coddy/statistics?keep=${keep}`, {
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
  const importHistory = async () => {
    setBusy(true);
    setError("");
    try {
      await statisticsRequest("/coddy/statistics/import", {
        method: "POST",
        body: JSON.stringify({ action: "start" }),
      });
      window.location.hash = "#/statistics";
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const changeRate = (index: number, field: keyof Rate, value: string) => {
    setSaved(false);
    setRates((old) =>
      old.map((r, i) => (i === index ? { ...r, [field]: value } : r)),
    );
  };
  const zones = [
    ...new Set([
      browserTimezone(),
      "UTC",
      "Europe/Moscow",
      "Europe/London",
      "America/New_York",
      "Asia/Tokyo",
    ]),
  ];
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
          <section
            data-settings-anchor={landmarkAnchor("statistics", "timezone")}
          >
            <h3 className="appearance-section-label" tabIndex={-1}>
              {t("stats.timezone")}
            </h3>
            <label className="statistics-filter">
              <span>{t("stats.timezone")}</span>
              <input
                list="statistics-timezones"
                value={timezone}
                placeholder={t("stats.timezone.browser")}
                onChange={(e) => {
                  setTimezone(e.target.value);
                  setSaved(false);
                }}
              />
              <datalist id="statistics-timezones">
                {zones.map((z) => (
                  <option key={z} value={z} />
                ))}
              </datalist>
            </label>
            <p className="statistics-muted">
              {t("stats.timezone.hint", { timezone: browserTimezone() })}
            </p>
          </section>
          <section
            data-settings-anchor={landmarkAnchor("statistics", "prices")}
          >
            <h3 className="appearance-section-label" tabIndex={-1}>
              {t("stats.prices")}
            </h3>
            <p className="statistics-muted">{t("stats.prices.hint")}</p>
            <datalist id="statistics-models">
              {models.map((m) => (
                <option key={m} value={m} />
              ))}
            </datalist>
            {rates.map((r, i) => (
              <div className="statistics-rate" key={i}>
                <label className="statistics-filter">
                  <span>{t("stats.model")}</span>
                  <input
                    list="statistics-models"
                    value={r.name}
                    onChange={(e) => changeRate(i, "name", e.target.value)}
                  />
                </label>
                {(["input", "output", "cached"] as const).map((field) => (
                  <label className="statistics-filter" key={field}>
                    <span>{t(`stats.${field}`)}</span>
                    <input
                      type="number"
                      min="0"
                      step="any"
                      value={r[field]}
                      onChange={(e) => changeRate(i, field, e.target.value)}
                    />
                  </label>
                ))}
                <button
                  aria-label={t("stats.prices.remove")}
                  onClick={() => {
                    setRates((old) => old.filter((_, j) => i !== j));
                    setSaved(false);
                  }}
                >
                  ×
                </button>
              </div>
            ))}
            <button
              onClick={() => {
                setRates((old) => [
                  ...old,
                  { name: "", input: "", output: "", cached: "" },
                ]);
                setSaved(false);
              }}
            >
              {t("stats.prices.add")}
            </button>
          </section>
          <button
            className="statistics-primary"
            disabled={busy}
            onClick={() => void save()}
          >
            {t(saved ? "stats.saved" : "stats.save")}
          </button>
          <section
            data-settings-anchor={landmarkAnchor("statistics", "import")}
          >
            <h3 className="appearance-section-label" tabIndex={-1}>
              {t("stats.import.heading")}
            </h3>
            <p className="statistics-muted">{t("stats.import.message")}</p>
            <button
              disabled={busy || settings.import.state === "running"}
              onClick={() => void importHistory()}
            >
              {t("stats.import.start")}
            </button>
          </section>
          <section data-settings-anchor={landmarkAnchor("statistics", "clear")}>
            <h3 className="appearance-section-label" tabIndex={-1}>
              {t("stats.clear.heading")}
            </h3>
            <p className="statistics-muted">{t("stats.clear.hint")}</p>
            <label className="statistics-filter">
              <span>{t("stats.clear.keep")}</span>
              <select value={keep} onChange={(e) => setKeep(e.target.value)}>
                {["none", "day", "week", "month"].map((v) => (
                  <option key={v} value={v}>
                    {t(`stats.clear.${v}`)}
                  </option>
                ))}
              </select>
            </label>
            <button
              className="statistics-danger"
              disabled={busy}
              onClick={() => setConfirm(true)}
            >
              {t("stats.clear.action")}
            </button>
          </section>
        </>
      )}
      <ConfirmDialog
        open={confirm}
        title={t("stats.clear.title")}
        message={t("stats.clear.message", { period: t(`stats.clear.${keep}`) })}
        confirmLabel={t("stats.clear.action")}
        variant="danger"
        ariaLabel={t("stats.clear.title")}
        onConfirm={() => void clear()}
        onCancel={() => setConfirm(false)}
        confirming={busy}
      />
    </div>
  );
}
