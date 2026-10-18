import type { SystemStatusDocument } from "./systemStatusApi";

/**
 * A status document as /coddy/status builds it, of a server with the metrics
 * view: 2 attention items, 240-point series. Members the contract omits when
 * empty are left out by passing `undefined`.
 */
export function fakeStatus(
  over: {
    [K in keyof SystemStatusDocument]?: SystemStatusDocument[K] | undefined;
  } = {},
): SystemStatusDocument {
  const series = (f: (i: number) => number | null) =>
    Array.from({ length: 240 }, (_, i) => f(i));
  const doc: SystemStatusDocument = {
    object: "coddy.status",
    generatedAt: "2026-10-15T10:00:00Z",
    build: {
      version: "1.3.12",
      revision: "050fbbbf0123456789",
      goVersion: "go1.26.9",
      os: "linux",
      arch: "arm64",
      tags: ["cli", "gateway", "http", "metrics", "ui"],
    },
    process: {
      startedAt: "2026-10-15T09:00:00Z",
      startedBy: "systemd",
      configFile: "~/.coddy/config.yaml",
    },
    subsystems: [
      {
        kind: "httpserver",
        state: "running",
        address: "127.0.0.1:12345",
        pendingAddress: "0.0.0.0:12345",
      },
      {
        kind: "metrics",
        state: "running",
        address: "127.0.0.1:12347",
        credential: "token",
      },
    ],
    restartRequired: true,
    turns: { running: 2, oldestSeconds: 340 },
    tasks: { agent: { running: 1 }, command: { queued: 1 } },
    disk: { availableBytes: 398_458_880, sizeBytes: 250_000_000_000 },
    attention: [{ code: "disk_low" }, { code: "restart_required" }],
    metrics: {
      providers: [
        {
          name: "local-mlx",
          attempts: 1204,
          failures: 4,
          meanSeconds: 6.1,
          lastFailureAt: "2026-10-15T09:48:00Z",
          lastOkAt: "2026-10-15T09:59:45Z",
        },
      ],
      series: {
        stepSeconds: 15,
        end: "2026-10-15T10:00:00Z",
        attemptsOk: series((i) => (i < 200 ? null : 3 + (i % 5))),
        providerErrors: series((i) => (i < 200 ? null : i % 7 === 0 ? 1 : 0)),
        attemptSecondsMean: series((i) => (i < 200 ? null : 5.8)),
        tokensIn: series((i) => (i < 200 ? null : 38_000)),
        tokensOut: series((i) => (i < 200 ? null : 2_100)),
        turnsRunning: series((i) => (i < 220 ? 1 : 2)),
        tasksUnfinished: series(() => 3),
      },
    },
  };
  // A member given as undefined is one the server leaves out.
  for (const [key, value] of Object.entries(over)) {
    if (value === undefined) {
      delete (doc as Record<string, unknown>)[key];
    } else {
      (doc as Record<string, unknown>)[key] = value;
    }
  }
  return doc;
}
