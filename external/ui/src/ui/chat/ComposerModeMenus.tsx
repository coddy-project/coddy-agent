import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import {
  filterLlmModels,
  groupLlmModelsByVendor,
  shouldGroupLlmModels,
  shouldShowLlmFilter,
} from "./llmModelMenu";
import { displayLlmId, displayModeLabel } from "./composerLabels";

/**
 * The portaled Mode / Model / Reasoning menu: bottom sheet on mobile shells,
 * anchored dropdown on desktop. The model branch owns its filter query.
 */
export function ComposerModeMenus(props: {
  menuOpen: "mode" | "llm" | "reasoning";
  menuAnchorRect: DOMRect | null;
  menuUseSheet: boolean;
  /** "opens-down" on the hero composer, "opens-up" when docked. */
  modeMenuDirClass: string;
  modes: string[];
  mode: string;
  onModeChange: (mode: string) => void;
  llmModels: string[];
  llmModel: string;
  onLlmModelChange?: ((modelId: string) => void) | undefined;
  reasoningLevels: string[];
  reasoning: string;
  onLlmReasoningChange?: ((level: string) => void) | undefined;
  onClose: () => void;
}) {
  const { t } = useT();
  const {
    menuOpen,
    menuAnchorRect,
    menuUseSheet,
    modeMenuDirClass,
    onClose: closeMenu,
  } = props;
  /** Live query for the model menu filter (only meaningful while `menuOpen === "llm"`). */
  const [llmQuery, setLlmQuery] = useState("");
  const llmFilterRef = useRef<HTMLInputElement | null>(null);

  // A fresh query per opened menu, matching the old reset-on-toggle behavior.
  useEffect(() => {
    setLlmQuery("");
  }, [menuOpen]);

  const llmList = props.llmModels;
  const llmVal = props.llmModel;
  // Filter input appears once the backend list is long; vendor grouping kicks
  // in whenever more than one vendor is configured. See llmModelMenu.ts.
  const llmShowFilter = shouldShowLlmFilter(llmList.length);
  const llmFiltered = useMemo(
    () => filterLlmModels(llmList, llmQuery),
    [llmList, llmQuery],
  );
  const llmGrouped = shouldGroupLlmModels(llmList);
  const llmGroups = useMemo(
    () => groupLlmModelsByVendor(llmFiltered),
    [llmFiltered],
  );
  const reasoningVal = props.reasoning;

  function renderLlmItem(mid: string) {
    return (
      <button
        key={mid}
        type="button"
        role="menuitem"
        title={mid}
        className={`mode-item ${mid === llmVal ? "is-selected" : ""}`}
        onClick={() => {
          props.onLlmModelChange?.(mid);
          closeMenu();
        }}
      >
        {displayLlmId(mid, t("composer.model"))}
      </button>
    );
  }

  if (!menuUseSheet && !menuAnchorRect) {
    return null;
  }

  return createPortal(
    <>
      <button
        type="button"
        className={`mode-menu-backdrop ${menuUseSheet ? "mode-menu-backdrop--scrim" : ""}`}
        aria-hidden="true"
        tabIndex={-1}
        onMouseDown={(e) => {
          e.preventDefault();
          closeMenu();
        }}
      />
      <div
        className={`mode-menu ${menuUseSheet ? "mode-menu--sheet" : `mode-menu--portal ${modeMenuDirClass}`} ${menuOpen === "llm" ? "mode-menu--llm" : ""}`}
        role="menu"
        style={
          menuUseSheet || !menuAnchorRect
            ? undefined
            : modeMenuDirClass === "opens-up"
              ? {
                  left: menuAnchorRect.left,
                  bottom: window.innerHeight - menuAnchorRect.top + 8,
                }
              : {
                  left: menuAnchorRect.left,
                  top: menuAnchorRect.bottom + 8,
                }
        }
      >
        {menuOpen === "mode"
          ? props.modes.map((m) => (
              <button
                key={m}
                type="button"
                role="menuitem"
                className={`mode-item ${m === props.mode ? "is-selected" : ""}`}
                onClick={() => {
                  props.onModeChange(m);
                  closeMenu();
                }}
              >
                {displayModeLabel(m, t)}
              </button>
            ))
          : null}
        {menuOpen === "llm" ? (
          <>
            {llmShowFilter ? (
              <input
                ref={llmFilterRef}
                type="text"
                className="mode-menu-filter"
                data-testid="model-menu-filter"
                aria-label={t("composer.filterModels")}
                placeholder={t("composer.filterModelsPlaceholder")}
                autoFocus
                value={llmQuery}
                onChange={(e) => setLlmQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    e.preventDefault();
                    e.stopPropagation();
                    closeMenu();
                  } else if (e.key === "Enter") {
                    e.preventDefault();
                    e.stopPropagation();
                    const first = llmFiltered[0];
                    if (first) {
                      props.onLlmModelChange?.(first);
                      closeMenu();
                    }
                  }
                }}
              />
            ) : null}
            <div className="mode-menu-scroll">
              {llmFiltered.length === 0 ? (
                <div className="mode-menu-empty" data-testid="model-menu-empty">
                  {t("composer.noModelsMatch", {
                    query: llmQuery.trim(),
                  })}
                </div>
              ) : llmGrouped ? (
                llmGroups.map((g) => (
                  <div key={g.vendor || "_"} className="mode-menu-group">
                    <div className="mode-menu-group-label">
                      {g.vendor || t("composer.vendorOther")}
                    </div>
                    {g.models.map((mid) => renderLlmItem(mid))}
                  </div>
                ))
              ) : (
                llmFiltered.map((mid) => renderLlmItem(mid))
              )}
            </div>
          </>
        ) : null}
        {menuOpen === "reasoning"
          ? props.reasoningLevels.map((lv) => (
              <button
                key={lv}
                type="button"
                role="menuitem"
                title={lv}
                className={`mode-item ${lv === reasoningVal ? "is-selected" : ""}`}
                onClick={() => {
                  props.onLlmReasoningChange?.(lv);
                  closeMenu();
                }}
              >
                {lv.slice(0, 1).toUpperCase() + lv.slice(1)}
              </button>
            ))
          : null}
      </div>
    </>,
    document.body,
  );
}
