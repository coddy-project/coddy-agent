import {
  RAIL_SCREENS,
  useRailScreenEscape,
  type RailScreenId,
  type RailScreens,
} from "./railEscape";

/**
 * A screen of the rail open under what a test renders, listening for Escape
 * the way App's are: render it first, so it listens from before the tip, menu
 * or popover under test opens, as a screen does. `onClose` hears what reaches
 * the screen.
 */
export function OpenRailScreen(props: {
  id?: RailScreenId;
  onClose: () => void;
}) {
  const open = props.id ?? "settings";
  const screens = {} as RailScreens;
  for (const id of RAIL_SCREENS) {
    screens[id] = {
      open: id === open,
      close: id === open ? props.onClose : () => {},
    };
  }
  useRailScreenEscape(screens);
  return null;
}
