package preview

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

// ToolPreviewServer is the tool name, kept in one place so the registry, the
// documentation and the tests cannot drift apart.
const ToolPreviewServer = "preview_server"

// defaultHost is where the server listens when no settings reached the tool.
const defaultHost = "127.0.0.1"

// previewMu serializes the check-and-launch section of the tool so two
// parallel calls for the same directory cannot both miss the running row and
// bind two ports.
var previewMu sync.Mutex

// liveServers maps a running server task to its server, so a repeated call can
// tell whether the directory it would reuse is still the directory that server
// opened - a build that deletes and recreates it must get a new server, not the
// old one's dead tree. Task ids are per-session, so the key carries the session
// too; entries go when the server's Wait returns, which is when its task leaves
// the running set.
var liveServers sync.Map // sessionID + "/" + task id -> *server

// RegisterBuiltins registers the preview tools via add.
func RegisterBuiltins(add func(*tooling.Tool)) {
	add(PreviewServerTool())
}

// PreviewServerTool serves a project directory on a free local port.
func PreviewServerTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolPreviewServer,
			Description: "Serve a directory of the project as a static website on a free localhost port and return its URL, " +
				"so the user can open the work in their own browser. Use it when the user asks to run, open, preview or try " +
				"HTML/JS/CSS work (\"run this on a web server, I want to click around myself\"), or when a page cannot work " +
				"from file:// (ES modules, fetch, workers). The server is a background task: it keeps running after this call " +
				"until it is stopped with " + shell.ToolBackgroundStop + ", the session is deleted, coddy exits, or the optional timeout elapses; " +
				shell.ToolBackgroundList + " shows it and " + shell.ToolBackgroundOutput + " returns its request log (a 404 there " +
				"explains a blank page). Files are read from disk on every request, so edits show on reload. Dot-files (.git, .env) " +
				"are never served. After starting it, give the user the URL and invite them to open it and try the page themselves.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type": "string",
						"description": "Directory to serve, relative to the working directory (default: the working directory itself). " +
							"A file is accepted too: its directory is served and the returned URL opens that file. " +
							"Must be inside the working directory.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Stop the server by itself after this many seconds. Leave it out unless the user asked for a time limit: by default the server runs until it is stopped.",
					},
				},
			},
		},
		Execute: executePreviewServer,
	}
}

type previewServerArgs struct {
	Path           string `json:"path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func executePreviewServer(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[previewServerArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if args.TimeoutSeconds < 0 {
		return "", fmt.Errorf("timeout_seconds must not be negative")
	}
	host, publicHost := defaultHost, ""
	if env != nil && env.PreviewServer != nil {
		if !env.PreviewServer.Enabled {
			return "", fmt.Errorf("the preview server is disabled (tools.preview_server.enable is false)")
		}
		if h := strings.TrimSpace(env.PreviewServer.Host); h != "" {
			host = h
		}
		publicHost = strings.TrimSpace(env.PreviewServer.PublicHost)
	}
	pool, err := shell.RequirePool(env)
	if err != nil {
		return "", err
	}

	root, openPath, err := resolveRoot(args.Path, env.CWD)
	if err != nil {
		return "", err
	}

	// One server per directory: a second "open it again" gets the address
	// that is already up instead of a second port over the same files. The
	// check and the launch are one critical section, so two parallel calls
	// cannot both miss the running row.
	previewMu.Lock()
	defer previewMu.Unlock()

	for _, snap := range pool.List(env.SessionID) {
		if snap.Kind != bgtask.KindServer || snap.Status.Finished() || snap.URL == "" || !sameDir(snap.CWD, root) {
			continue
		}
		live, ok := liveServers.Load(env.SessionID + "/" + snap.ID)
		if !ok {
			// A running server task with no registry entry is one this process
			// did not launch or one that is already dying - either way it is
			// not the address to reuse; a fresh server serves the directory.
			continue
		}
		if live.(*server).servesSameDir(root) {
			return describe(snap, openPath, true), nil
		}
		// The directory was replaced since this server opened it: its answers
		// come from the deleted tree. Stop it and serve fresh.
		_, _ = pool.Stop(env.SessionID, snap.ID)
	}

	srv, err := listen(root, host, publicHost)
	if err != nil {
		return "", err
	}
	snap, err := pool.Launch(bgtask.Spec{
		SessionID:      env.SessionID,
		Kind:           bgtask.KindServer,
		Label:          "preview " + displayRoot(root, env.CWD),
		CWD:            root,
		ToolCallID:     env.ToolCallID,
		URL:            srv.url(),
		NoTimeout:      true,
		TimeoutSeconds: args.TimeoutSeconds,
	}, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		srv.registryKey = env.SessionID + "/" + taskID
		srv.serve(out)
		liveServers.Store(srv.registryKey, srv)
		return srv, nil
	})
	if err != nil {
		// Refused before the callback ran (the session is at its task limit,
		// coddy is shutting down): the port must not stay taken.
		srv.close()
		return "", err
	}
	return describe(snap, openPath, false), nil
}

// resolveRoot turns the path argument into the directory to serve and, when the
// argument named a file, the URL path that opens it. The directory has to be
// the working directory or inside it, symlinks resolved: the tool runs without
// a permission prompt, which is only reasonable for files the session already
// works with.
func resolveRoot(arg, cwd string) (root, openPath string, err error) {
	if strings.TrimSpace(cwd) == "" {
		return "", "", fmt.Errorf("the session has no working directory to serve")
	}
	target := cwd
	if strings.TrimSpace(arg) != "" {
		target = toolfs.ResolvePath(strings.TrimSpace(arg), cwd)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", "", fmt.Errorf("cannot serve %s: %w", arg, err)
	}
	if !info.IsDir() {
		openPath = filepath.Base(target)
		if strings.HasPrefix(openPath, ".") {
			return "", "", fmt.Errorf("%s is a dot-file; the preview server never serves dot-files", target)
		}
		target = filepath.Dir(target)
	}
	realRoot, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", fmt.Errorf("cannot serve %s: %w", arg, err)
	}
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", fmt.Errorf("working directory: %w", err)
	}
	rel, err := filepath.Rel(realCWD, realRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s is outside the working directory %s; the preview server only serves the project", target, cwd)
	}
	// A path inside a dot-named element is never served either: the same rule
	// the handler applies to the URL holds for the root it is handed.
	if hiddenPathElement(filepath.ToSlash(rel)) {
		return "", "", fmt.Errorf("%s is inside a dot-named path; the preview server never serves dot-files", target)
	}
	return realRoot, openPath, nil
}

// sameDir compares two resolved directories the way the host file system does.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// displayRoot names the served directory relative to the working directory,
// and the working directory itself by its own name: "preview ." says nothing.
func displayRoot(root, cwd string) string {
	if realCWD, err := filepath.EvalSymlinks(cwd); err == nil {
		if rel, err := filepath.Rel(realCWD, root); err == nil && rel != "." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(root)
}

// describe is the tool result. It is written for the model, and its job is to
// get the address in front of the user with an invitation to open it.
func describe(snap bgtask.Snapshot, openPath string, reused bool) string {
	address := snap.URL
	if openPath != "" {
		address += url.PathEscape(openPath)
	}
	var b strings.Builder
	if reused {
		fmt.Fprintf(&b, "The preview server for this directory is already running: %s\n", address)
	} else {
		fmt.Fprintf(&b, "Preview server started: %s\n", address)
	}
	fmt.Fprintf(&b, "Serving %s as background task %s.\n", snap.CWD, snap.ID)
	fmt.Fprintf(&b, "Give the user this link and invite them to open %s in their browser and try it themselves.\n", address)
	if snap.TimeoutSeconds > 0 {
		fmt.Fprintf(&b, "It stops by itself %ds after it started, or earlier with %s.", snap.TimeoutSeconds, shell.ToolBackgroundStop)
	} else {
		fmt.Fprintf(&b, "It runs until it is stopped with %s, the session is deleted or coddy exits.", shell.ToolBackgroundStop)
	}
	fmt.Fprintf(&b, " Files are read on every request, so a reload shows your edits; %s returns the request log.", shell.ToolBackgroundOutput)
	return b.String()
}
