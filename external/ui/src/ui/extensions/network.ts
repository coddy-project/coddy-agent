export type ExtensionFetchOptions = {
  method?: string;
  headers?: HeadersInit;
  body?:
    | string
    | Blob
    | ArrayBuffer
    | Uint8Array<ArrayBuffer>
    | URLSearchParams;
  signal?: AbortSignal;
};

export type ExtensionNetwork = {
  fetch: (url: string, options?: ExtensionFetchOptions) => Promise<Response>;
};

function base64(bytes: Uint8Array) {
  let binary = "";
  for (let i = 0; i < bytes.length; i += 32768)
    binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
  return btoa(binary);
}

/** HTTP bytes travel in JSON, so target headers and HTML never become a
 * response of the application origin. The environment shim authenticates only
 * the outer request; the caller explicitly supplies any target credentials. */
export function extensionNetwork(
  id: string,
  sha256: string,
  lifetime: AbortSignal,
): ExtensionNetwork {
  return {
    async fetch(url, options = {}) {
      const controller = new AbortController();
      const abort = () => controller.abort();
      const signals = [lifetime, options.signal].filter(
        (signal): signal is AbortSignal => !!signal,
      );
      signals.forEach((signal) => {
        if (signal.aborted) abort();
        else signal.addEventListener("abort", abort, { once: true });
      });
      try {
        controller.signal.throwIfAborted();
        const headers = new Headers(options.headers);
        let body: string | undefined;
        if (options.body !== undefined) {
          const form = options.body instanceof URLSearchParams;
          const encoded = new Response(
            form ? options.body.toString() : options.body,
          );
          const bytes = new Uint8Array(await encoded.arrayBuffer());
          controller.signal.throwIfAborted();
          if (bytes.byteLength > 4 * 1024 * 1024)
            throw new Error("Extension request body exceeds 4 MiB");
          body = base64(bytes);
          if (!headers.has("Content-Type") && form)
            headers.set(
              "Content-Type",
              "application/x-www-form-urlencoded;charset=UTF-8",
            );
          else if (
            !headers.has("Content-Type") &&
            encoded.headers.has("Content-Type")
          )
            headers.set("Content-Type", encoded.headers.get("Content-Type")!);
        }
        const response = await fetch(
          `/coddy/extensions/${encodeURIComponent(id)}/fetch`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            signal: controller.signal,
            body: JSON.stringify({
              sha256,
              url,
              method: (options.method ?? "GET").toUpperCase(),
              headers: Object.fromEntries(headers),
              body,
            }),
          },
        );
        if (!response.ok)
          throw new Error(
            `Extension proxy: HTTP ${response.status}: ${(await response.text()).slice(0, 1000)}`,
          );
        const result = (await response.json()) as {
          status: number;
          status_text: string;
          headers: Record<string, string[]>;
          body: string | null;
        };
        controller.signal.throwIfAborted();
        const targetHeaders = new Headers();
        for (const [name, values] of Object.entries(result.headers))
          values.forEach((value) => targetHeaders.append(name, value));
        const bytes = Uint8Array.from(atob(result.body ?? ""), (c) =>
          c.charCodeAt(0),
        );
        return new Response(
          options.method?.toUpperCase() === "HEAD" ||
            [204, 205, 304].includes(result.status)
            ? null
            : bytes,
          {
            status: result.status,
            statusText: result.status_text,
            headers: targetHeaders,
          },
        );
      } finally {
        signals.forEach((signal) => signal.removeEventListener("abort", abort));
      }
    },
  };
}
