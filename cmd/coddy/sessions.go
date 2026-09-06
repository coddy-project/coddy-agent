package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// sessionsExportUsage is the usage line of `coddy sessions export`.
func sessionsExportUsage() string {
	return fmt.Sprintf("usage: %s sessions export <session-id> [--format md|html|json|jsonl] [--out <path>] [--no-tools] [--no-thinking] [--sessions-dir <path>]", os.Args[0])
}

// parseAroundPositional parses flags that may sit before or after the single
// positional argument (`export <id> --format json` and `export --format json
// <id>` both work) and returns that argument.
func parseAroundPositional(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", nil
	}
	positional := rest[0]
	if len(rest) > 1 {
		if err := fs.Parse(rest[1:]); err != nil {
			return "", err
		}
		if fs.NArg() > 0 {
			return "", fmt.Errorf("unexpected argument %q", fs.Arg(0))
		}
	}
	return positional, nil
}

// sessionsExport implements `coddy sessions export`, the shell twin of the
// built-in /export command. It reads a stored bundle (an exact id or a unique
// prefix), builds the same export document, and writes it under cwd or
// wherever --out points. A nil store is opened from --sessions-dir or the
// configuration.
func sessionsExport(w io.Writer, store *session.FileStore, cfg *config.Config, cwd string, args []string) error {
	fs := flag.NewFlagSet("sessions export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	format := fs.String("format", "", "export format: md, html, json, jsonl (default: from the --out extension, else md)")
	out := fs.String("out", "", "output file or directory (default: the current directory)")
	noTools := fs.Bool("no-tools", false, "leave out tool calls and their results")
	noThinking := fs.Bool("no-thinking", false, "leave out the model's reasoning")
	rootFlag := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")

	id, err := parseAroundPositional(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(w, sessionsExportUsage())
			fs.SetOutput(w)
			fs.PrintDefaults()
			return nil
		}
		return fmt.Errorf("%w\n%s", err, sessionsExportUsage())
	}
	if strings.TrimSpace(id) == "" {
		return errors.New(sessionsExportUsage())
	}
	if store == nil {
		store, err = openSessionStore(*rootFlag, cfg)
		if err != nil {
			return err
		}
	}
	resolved, err := store.ResolveSessionID(id)
	if err != nil {
		return err
	}
	snap, err := store.ReadSnapshot(resolved)
	if err != nil {
		return err
	}
	root, target, err := session.PrepareExportOutput(cwd, *out)
	if err != nil {
		return err
	}

	// A detached State reuses the title and model rules of a live session.
	st := &session.State{
		ID:              resolved,
		CWD:             snap.Meta.CWD,
		Mode:            session.Mode(snap.Meta.Mode),
		SelectedModelID: snap.Meta.SelectedModelID,
		Messages:        snap.Messages,
	}
	st.SetTitlePinnedWithoutPersist(snap.Meta.TitlePinned)
	title := strings.TrimSpace(snap.Meta.Title)
	if title == "" {
		title = st.ConversationTitle()
	}
	in := session.ExportInput{
		SessionID:  resolved,
		Title:      title,
		CWD:        snap.Meta.CWD,
		Model:      st.EffectiveModelID(cfg),
		Messages:   snap.Messages,
		ExportedAt: time.Now().UTC(),
		OutputRoot: root,
	}
	if ws := strings.TrimSpace(snap.Meta.CWD); ws != "" {
		if fi, statErr := os.Stat(ws); statErr == nil && fi.IsDir() {
			in.GitBranch = gitws.Describe(ws).Branch
		}
	}
	if stats, statsErr := session.ReadSessionStats(snap.Dir); statsErr == nil {
		in.Stats = stats
	}
	res, err := session.ExportSession(in, session.ExportRequest{
		Format:  *format,
		Target:  target,
		Options: session.ExportOptions{NoTools: *noTools, NoThinking: *noThinking},
	})
	if err != nil {
		return err
	}
	path := res.Target.Path
	if abs, absErr := filepath.Abs(path); absErr == nil {
		path = abs
	}
	_, _ = fmt.Fprintf(w, "Session exported to %s: %s (%d transcript entries)\n", res.Format.DisplayName(), path, res.Entries)
	return nil
}
