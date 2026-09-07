/** `error.code` the composer relay uses for "no turn is running for this session". */
export const NO_ACTIVE_STREAM_CODE = "no_active_stream";

const NO_ACTIVE_STREAM_MESSAGE = "no active composer stream";

/**
 * Whether a composer-stream error means "there is nothing to watch" rather than a failure.
 *
 * A re-attach that finds the turn already finished is an ordinary outcome: the caller should
 * reconcile from the persisted transcript instead of showing the user an error row. The
 * message check covers servers that predate the `error.code` field.
 */
export function isNoLiveTurnRelayError(
  code: string | null | undefined,
  message: string | null | undefined,
): boolean {
  if (typeof code === "string" && code.trim() === NO_ACTIVE_STREAM_CODE) {
    return true;
  }
  return (
    typeof message === "string" &&
    message.trim().toLowerCase() === NO_ACTIVE_STREAM_MESSAGE
  );
}
