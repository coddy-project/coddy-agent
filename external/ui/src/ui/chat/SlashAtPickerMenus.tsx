import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { workspacePickRowSubtitle } from "../skills/workspacePickRowSubtitle";
import type { PickerFloatRect } from "./composerPickerApi";
import type { useSlashAtPickers } from "./useSlashAtPickers";

/**
 * The portaled `/` and `@` picker menus: bottom sheet on stacked shells,
 * floating panel anchored to the composer field wrap elsewhere.
 */
export function SlashAtPickerMenus({
  picker,
  pickerUseSheet,
  pickerFloatRect,
  sheetBottomPx,
  isEmpty,
}: {
  picker: ReturnType<typeof useSlashAtPickers>;
  pickerUseSheet: boolean;
  pickerFloatRect: PickerFloatRect | null;
  sheetBottomPx: number | null;
  isEmpty: boolean;
}) {
  const { t } = useT();
  const {
    atOpen,
    pickerOpen,
    slashItems,
    commandMatches,
    slashActiveIdx,
    setSlashActive,
    showSkillsSection,
    slashLoading,
    slashErr,
    slashHasMore,
    atItems,
    atPrefix,
    atLoading,
    atErr,
    atHasMore,
    applySlashChoice,
    applyAtChoice,
    loadMoreSlash,
    loadMoreAt,
    dismissSlashAtPickers,
  } = picker;

  const slashMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        {showSkillsSection ? (
          <>
            <div className="slash-menu-title">{t("composer.skillsTitle")}</div>
            {slashLoading && slashItems.length === 0 ? (
              <div className="slash-muted">{t("composer.loading")}</div>
            ) : null}
            {slashErr ? <div className="slash-err">{slashErr}</div> : null}
            {!slashLoading && slashItems.length === 0 && !slashErr ? (
              <div className="slash-muted">
                {t("composer.noMatchingSkills")}
              </div>
            ) : null}
            <ul className="slash-rows">
              {slashItems.map((row, idx) => (
                <li key={row.name}>
                  <button
                    type="button"
                    role="option"
                    aria-selected={idx === slashActiveIdx}
                    className={`slash-row-btn${idx === slashActiveIdx ? " is-active" : ""}`}
                    data-testid={`slash-command-row-${row.name}`}
                    onMouseEnter={() => setSlashActive(idx)}
                    onMouseDown={(e) => {
                      e.preventDefault();
                      applySlashChoice(row.name);
                    }}
                  >
                    <span className="slash-row-line">
                      <span className="slash-row-name">/{row.name}</span>
                      {row.description ? (
                        <>
                          {" "}
                          <span className="slash-row-desc">
                            {row.description}
                          </span>
                        </>
                      ) : null}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
            {slashHasMore ? (
              <button
                type="button"
                className="slash-load-more"
                disabled={slashLoading}
                data-testid="slash-command-more"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => loadMoreSlash()}
              >
                {slashLoading ? t("composer.loading") : t("composer.more")}
              </button>
            ) : null}
          </>
        ) : null}
        {commandMatches.length > 0 ? (
          <>
            <div className="slash-menu-title">
              {t("composer.commandsTitle")}
            </div>
            <ul className="slash-rows">
              {commandMatches.map((row, idx) => {
                const gidx = slashItems.length + idx;
                return (
                  <li key={`cmd-${row.name}`}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={gidx === slashActiveIdx}
                      className={`slash-row-btn${gidx === slashActiveIdx ? " is-active" : ""}`}
                      data-testid={`command-row-${row.name}`}
                      onMouseEnter={() => setSlashActive(gidx)}
                      onMouseDown={(e) => {
                        e.preventDefault();
                        applySlashChoice(row.name);
                      }}
                    >
                      <span className="slash-row-line">
                        <span className="slash-row-name">/{row.name}</span>
                        {row.description ? (
                          <>
                            {" "}
                            <span className="slash-row-desc">
                              {row.description}
                            </span>
                          </>
                        ) : null}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </>
        ) : null}
      </div>
    </>
  );

  const atMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        <div className="slash-menu-title">
          {t("composer.workspaceFilesTitle")}
        </div>
        {atPrefix.trim() === "" && atItems.length === 0 ? (
          <div className="slash-muted">{t("composer.typeAfterAt")}</div>
        ) : null}
        {atLoading && atItems.length === 0 && atPrefix.trim() !== "" ? (
          <div className="slash-muted">{t("composer.loading")}</div>
        ) : null}
        {atErr ? <div className="slash-err">{atErr}</div> : null}
        {!atLoading &&
        atItems.length === 0 &&
        !atErr &&
        atPrefix.trim() !== "" ? (
          <div className="slash-muted">{t("composer.noFiles")}</div>
        ) : null}
        <ul className="slash-rows">
          {atItems.map((row) => (
            <li key={`${row.kind}:${row.path_rel}`}>
              <button
                type="button"
                role="option"
                className="slash-row-btn"
                data-testid={`workspace-file-row-${row.path_rel.replace(/[^a-zA-Z0-9_-]+/g, "_")}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  applyAtChoice(row);
                }}
              >
                <span className="slash-row-name">@{row.path_rel}</span>
                <span className="slash-row-desc">
                  {workspacePickRowSubtitle(row)}
                </span>
              </button>
            </li>
          ))}
        </ul>
        {atHasMore ? (
          <button
            type="button"
            className="slash-load-more"
            disabled={atLoading}
            data-testid="workspace-files-more"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => loadMoreAt()}
          >
            {atLoading ? t("composer.loading") : t("composer.more")}
          </button>
        ) : null}
      </div>
    </>
  );

  if (!pickerOpen) {
    return null;
  }

  return createPortal(
    pickerUseSheet ? (
      <>
        <button
          type="button"
          className="slash-sheet-backdrop"
          aria-label={t("composer.closePicker")}
          tabIndex={-1}
          onMouseDown={(e) => {
            e.preventDefault();
            dismissSlashAtPickers();
          }}
        />
        <div
          className={[
            "slash-menu slash-menu--sheet",
            !isEmpty ? "slash-menu--above-composer" : "",
          ]
            .filter(Boolean)
            .join(" ")}
          data-testid={atOpen ? "workspace-files-menu" : "slash-command-menu"}
          role="listbox"
          aria-label={
            atOpen
              ? t("composer.workspaceFilesAriaLabel")
              : t("composer.slashCommandsAriaLabel")
          }
          style={
            !isEmpty && sheetBottomPx != null
              ? {
                  bottom: sheetBottomPx,
                  ["--context-sheet-bottom" as string]: `${sheetBottomPx}px`,
                }
              : undefined
          }
        >
          {atOpen ? atMenuChrome : slashMenuChrome}
        </div>
      </>
    ) : pickerFloatRect ? (
      <div
        className="slash-menu slash-menu--portal"
        data-testid={atOpen ? "workspace-files-menu" : "slash-command-menu"}
        role="listbox"
        aria-label={
          atOpen
            ? t("composer.workspaceFilesAriaLabel")
            : t("composer.slashCommandsAriaLabel")
        }
        style={{
          left: pickerFloatRect.left,
          width: pickerFloatRect.width,
          bottom: pickerFloatRect.bottom,
        }}
      >
        {atOpen ? atMenuChrome : slashMenuChrome}
      </div>
    ) : null,
    document.body,
  );
}
