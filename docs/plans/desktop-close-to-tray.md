# Closing the desktop window to the tray

## Scope

Change the desktop shell's close action on Linux. When **Show the tray icon** is enabled and the
icon is present, the close button minimises the window and leaves the Coddy process and its HTTP
surface running. With the tray disabled, the same button closes the window and ends the run.

This change does not add a tray on macOS or Windows, change the browser's Dock icon, introduce a
native window backend or alter the HTTP API. The system window manager's own close button remains
outside this browser-backend change.

## Design

`WindowHandle` gains a close-request operation for the shell. It reads the attached
`SettingsStore`. A requested close minimises the window only when the tray setting is enabled and
the tray reports itself visible. Otherwise it follows the existing close operation.

The shell keeps its current `POST /desktop/window` action and calls the close-request operation
through a small interface assertion. Other `WindowControl` implementations retain their current
close behavior, and the wire format does not change.

`SettingsStore.PickMenu(MenuQuit)` continues to call the real close operation. **Quit Coddy** must
end the application even when the tray is enabled. The existing tray activation and **Show the
window** actions restore a minimised window without new state.

## Failure handling

A saved tray preference does not prove that the current session published its icon. If the tray is
absent, disabled or failed to appear, a close request closes the application instead of leaving an
inaccessible background process. Errors from minimising or closing continue to surface through the
existing HTTP handler.

## Tests

Extend `features/desktop_window.feature` with the two happy paths: close with a visible tray leaves
the run alive and minimises the window; close without the tray ends the run. Drive the real shell
route and `WindowHandle` with recording window controls.

Add unit coverage for a requested close when a saved tray preference could not be shown, and for
**Quit Coddy** bypassing minimisation. Run the new tests before the implementation and confirm the
expected failures, then run the desktop package tests, `make test`, documentation checks and
`make lint`.

## Documentation

Update the desktop guide in English and Russian. The guide will distinguish the shell's close
button from **Quit Coddy** and state the fallback when the tray cannot be shown. The macOS icon
report is excluded from this change.
