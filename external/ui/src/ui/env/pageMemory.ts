// What the page learns once and keeps while it is open, across the switches
// between two remotes that start the app over in place (EnvScope). Started
// over, the app would otherwise draw every one of these as "not known yet" for
// a moment - the rail without Scheduler or Swarm, the map without this machine
// or without a picture at all - which is the blink a switch in place is for
// avoiding. A reload forgets all of it, as it forgets everything; so does every
// test (vitest.setup.ts).

import type {
  SwarmInfo,
  SwarmNode,
  SwarmSession,
  SwarmTopology,
} from "../swarm/types";

/** What serves this page: it never changes while the page is open. */
export type PageServer = {
  kind: "agent" | "relay";
  /** The host name of the machine, for the swarm map; empty when unknown. */
  host: string;
};

/** What the swarm map last showed for one relay. */
export type SwarmPicture = {
  info: SwarmInfo;
  nodes: SwarmNode[];
  sessions: SwarmSession[];
  warnings: string[];
  topology: SwarmTopology | null;
};

let pageServer: PageServer | null = null;
/** Base URLs known to be a relay's own home, by the last probe or a switch. */
const relayHomes = new Set<string>();
/** Whether a server has the scheduler, by its environment key. */
const schedulerLinked = new Map<string, boolean>();
/** Some server of this page said it has the scheduler. */
let schedulerSeen = false;
/** What the map last showed, by the relay it read. */
const swarmPictures = new Map<string, SwarmPicture>();

function trimBase(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

export function knownPageServer(): PageServer | null {
  return pageServer;
}

export function rememberPageServer(next: PageServer): void {
  pageServer = next;
}

/** isKnownRelayHome says whether a base URL was last found to be a relay. */
export function isKnownRelayHome(baseUrl: string): boolean {
  return relayHomes.has(trimBase(baseUrl));
}

export function rememberRelayHome(baseUrl: string, relay: boolean): void {
  const key = trimBase(baseUrl);
  if (relay) {
    relayHomes.add(key);
  } else {
    relayHomes.delete(key);
  }
}

/**
 * schedulerLinkedGuess is the best guess before the server answers: what it
 * said last time, else "yes" once any server of this page had one - the nodes
 * of one swarm mostly run one build - else nothing. "No" is taken only from
 * the server that said it: a relay has no scheduler by design, and a guessed
 * "no" drops a #/scheduler route before the node could say it has one.
 */
export function schedulerLinkedGuess(envKey: string): boolean | null {
  const known = schedulerLinked.get(envKey);
  if (known !== undefined) {
    return known;
  }
  return schedulerSeen ? true : null;
}

export function rememberSchedulerLinked(envKey: string, linked: boolean): void {
  schedulerLinked.set(envKey, linked);
  if (linked) {
    schedulerSeen = true;
  }
}

export function swarmPicture(relayKey: string): SwarmPicture | undefined {
  return swarmPictures.get(relayKey);
}

export function rememberSwarmPicture(
  relayKey: string,
  picture: SwarmPicture,
): void {
  swarmPictures.set(relayKey, picture);
}

/** resetPageMemoryForTests forgets everything, as a fresh page starts. */
export function resetPageMemoryForTests(): void {
  pageServer = null;
  relayHomes.clear();
  schedulerLinked.clear();
  schedulerSeen = false;
  swarmPictures.clear();
}
