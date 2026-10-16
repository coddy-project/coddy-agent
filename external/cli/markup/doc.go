//go:build cli

// Package markup is the picture editor the console opens for an image pasted
// from the clipboard when a graphical display is there: pencil, ellipse,
// rectangle, arrow and text in one of seven colours, crop, a quarter turn and
// undo, then Done or Cancel.
//
// The package is the whole editor but the window: the document and its
// history (document.go), the shapes rasterised in software (raster.go,
// text.go), the controller that turns pointer and key events into edits
// (editor.go) and the frame it paints - toolbar, palette, buttons and the
// picture fitted into the rest (layout.go, frame.go). A window system only
// has to show Editor.Frame and feed events back; the X11 one is
// external/cli/markup/xwindow. Everything here is plain Go over image.RGBA,
// so it is tested without a display.
package markup
