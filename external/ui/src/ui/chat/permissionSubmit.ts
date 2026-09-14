const HDR = "X-Coddy-Session-ID";

/**
 * POST a permission choice to /coddy/sessions/{id}/permission. Shared by the
 * inline permission card in the transcript and the card a detached subagent's
 * prompt gets on its task row, so both resolve a prompt the same way. Network
 * errors propagate; each caller decides what an unreachable server means for
 * its own surface.
 */
export async function submitPermissionChoice(
  sessionId: string,
  toolCallId: string,
  optionId: string,
): Promise<void> {
  const sid = sessionId.trim();
  const tcid = toolCallId.trim();
  await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/permission`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [HDR]: sid,
    },
    body: JSON.stringify({ toolCallId: tcid, optionId }),
  });
}
