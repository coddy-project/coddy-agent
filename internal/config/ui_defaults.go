package config

// UIThemeEnvVar names the environment variable that picks the web UI's default
// theme: a browser without its own coddy_ui_theme cookie opens on this theme.
// Served to the SPA as "ui.theme" in GET /coddy/info; an unknown name is
// ignored by the client.
const UIThemeEnvVar = "CODDY_UI_THEME"

// UIWallpaperEnvVar names the environment variable that picks the web UI's
// default wallpaper: a file name of the server's wallpaper gallery
// (${CODDY_HOME}/wallpapers), shown in a browser that has not chosen a
// background of its own. Served as "ui.wallpaper" in GET /coddy/info; when
// unset or empty, the plain theme background stays.
const UIWallpaperEnvVar = "CODDY_UI_WALLPAPER"
