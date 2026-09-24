import type { Ref } from "react";

import { useT } from "../i18n/I18nProvider";

/**
 * The top of a transcript that shows only its newest part (issue #338): the
 * rows above are on the server or not rendered yet. The control is also the
 * sentinel the transcript window watches, so it renders them by itself as the
 * reader scrolls up to it; the button is there for a keyboard, and for a read
 * that failed. See DESIGN.md, Transcript window.
 */
export function TranscriptEarlierControl(props: {
  state: "idle" | "loading" | "error";
  onShow: () => void;
  sentinelRef: Ref<HTMLDivElement>;
}) {
  const { t } = useT();
  return (
    <div
      ref={props.sentinelRef}
      className="transcript-earlier"
      data-testid="transcript-earlier"
      data-state={props.state}
    >
      {props.state === "loading" ? (
        <span className="transcript-earlier-status" role="status">
          {t("chat.transcriptEarlier.loading")}
        </span>
      ) : props.state === "error" ? (
        <>
          <span className="transcript-earlier-status" role="alert">
            {t("chat.transcriptEarlier.failed")}
          </span>
          <button
            type="button"
            className="transcript-earlier-button"
            onClick={props.onShow}
          >
            {t("chat.transcriptEarlier.retry")}
          </button>
        </>
      ) : (
        <button
          type="button"
          className="transcript-earlier-button"
          onClick={props.onShow}
        >
          {t("chat.transcriptEarlier.show")}
        </button>
      )}
    </div>
  );
}
