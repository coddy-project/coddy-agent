export type ExtensionBrowserNetwork = {
  browserFetch: (
    input: RequestInfo | URL,
    init?: RequestInit,
  ) => Promise<Response>;
  webSocket: (
    url: string | URL,
    protocols?: string | string[],
    options?: { signal?: AbortSignal },
  ) => WebSocket;
};

export type ExtensionBrowserNetworkOptions = {
  fetch?: typeof fetch;
  WebSocket?: typeof WebSocket;
};

function abortError() {
  return new DOMException("The operation was aborted", "AbortError");
}

function signalsFor(lifetime: AbortSignal, caller?: AbortSignal | null) {
  return caller && caller !== lifetime ? [lifetime, caller] : [lifetime];
}

function throwIfAborted(signals: AbortSignal[]) {
  if (signals.some((signal) => signal.aborted)) throw abortError();
}

/** Direct browser transports keep the browser's CORS, cookie and header rules.
 * The proxied extension fetch is deliberately not involved. */
export function extensionBrowserNetwork(
  lifetime: AbortSignal,
  options: ExtensionBrowserNetworkOptions = {},
): ExtensionBrowserNetwork {
  const fetcher: typeof fetch =
    options.fetch ?? ((input, init) => globalThis.fetch(input, init));
  const Socket = options.WebSocket ?? globalThis.WebSocket;
  return {
    async browserFetch(input, init) {
      const callerSignal =
        init?.signal ?? (input instanceof Request ? input.signal : undefined);
      const signals = signalsFor(lifetime, callerSignal);
      throwIfAborted(signals);
      const signal = signals[0]!;
      if (signals.length === 1) return fetcher(input, { ...init, signal });
      // A native dependent signal outlives the fetch promise, so aborting after
      // the headers still cancels the streamed body without wrapping Response.
      if (typeof AbortSignal.any === "function")
        return fetcher(input, { ...init, signal: AbortSignal.any(signals) });

      // Chromium before 116 has no AbortSignal.any. Keep this relay's listeners
      // after the headers so it can still cancel the native streamed body. They
      // are removed on abort or fetch failure; after an uneventful natural EOF
      // they remain until the extension lifetime ends, since observing that EOF
      // would require consuming or wrapping the native Response.
      const controller = new AbortController();
      const cleanup = () =>
        signals.forEach((signal) => signal.removeEventListener("abort", abort));
      const abort = () => {
        controller.abort();
        cleanup();
      };
      signals.forEach((signal) =>
        signal.addEventListener("abort", abort, { once: true }),
      );
      try {
        if (signals.some((signal) => signal.aborted)) abort();
        controller.signal.throwIfAborted();
        return await fetcher(input, { ...init, signal: controller.signal });
      } catch (error) {
        cleanup();
        throw error;
      }
    },
    webSocket(url, protocols, options = {}) {
      const signals = signalsFor(lifetime, options.signal);
      throwIfAborted(signals);
      const socket =
        protocols === undefined ? new Socket(url) : new Socket(url, protocols);
      const abort = () => socket.close();
      const cleanup = () => {
        socket.removeEventListener("close", cleanup);
        signals.forEach((signal) => signal.removeEventListener("abort", abort));
      };
      socket.addEventListener("close", cleanup, { once: true });
      signals.forEach((signal) =>
        signal.addEventListener("abort", abort, { once: true }),
      );
      // Close the race between the initial check and listener installation.
      if (signals.some((signal) => signal.aborted)) abort();
      return socket;
    },
  };
}
