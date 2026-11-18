/**
 * The statistics settings are saved by the Save of the settings drawer, like
 * every other tab, although they live outside `config.yaml`: the time zone and
 * the tariffs belong to the machine's own statistics store, not to the
 * configuration document the drawer writes. The panel registers what it holds
 * here while it is mounted, and the drawer asks this module whether anything is
 * unsaved and flushes it when the person presses Save.
 */

export type StatisticsSettingsHandle = {
  dirty: boolean;
  save: () => Promise<void>;
};

let handle: StatisticsSettingsHandle | null = null;
const listeners = new Set<() => void>();

function notify() {
  for (const fn of listeners) fn();
}

/** The panel calls this while it is mounted, and with null when it leaves. */
export function registerStatisticsSettings(
  next: StatisticsSettingsHandle | null,
) {
  handle = next;
  notify();
}

export function subscribeStatisticsSettings(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

export function statisticsSettingsDirty() {
  return handle?.dirty === true;
}

/** Always false on the server, where no panel is mounted. */
export function serverStatisticsSettingsDirty() {
  return false;
}

/** Saves what the panel holds. Does nothing when no panel is mounted. */
export async function flushStatisticsSettings() {
  await handle?.save();
}
