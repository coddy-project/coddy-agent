package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const sessionsMutationHelp = "Updates and deletes require a running local coddy serve or desktop runtime using the same sessions root. Stop any standalone console or ACP process sharing that root before changing its sessions."

func sessionsUsage() string {
	return fmt.Sprintf(`usage: %[1]s sessions list [--sessions-dir DIR] [--cwd DIR] [--origin local|gateway|print] [--format text|json]
       %[1]s sessions show <id> [--sessions-dir DIR] [--format text|json]
       %[1]s sessions update <id> [--title TEXT] [--tags TAGS] [--archived=true|false] [--sessions-dir DIR] [--format text|json]
       %[1]s sessions delete <id> [--yes] [--sessions-dir DIR] [--format text|json]
       %[1]s sessions export <id> [flags]

Show, update and delete accept an exact ID or a unique prefix, including archived sessions.
%[2]s
To create a session, start coddy and send a prompt, or run coddy -p "your prompt".
There is no sessions create command; opening a new chat alone does not save an empty session.`, os.Args[0], sessionsMutationHelp)
}

type sessionsCommand struct {
	out, errOut io.Writer
	in          io.Reader
	interactive bool
	mutate      func(context.Context, string, string, string, string, map[string]any) (json.RawMessage, error)
}

func localSessionsCommand() sessionsCommand {
	return sessionsCommand{
		out: os.Stdout, errOut: os.Stderr, in: os.Stdin,
		interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd())),
		mutate:      mutateStoredSession,
	}
}

func (c sessionsCommand) run(args []string) error {
	if len(args) == 0 {
		return errors.New(sessionsUsage())
	}
	verb := strings.TrimSpace(args[0])
	if verb == "-h" || verb == "--help" {
		_, err := fmt.Fprintln(c.out, sessionsUsage())
		return err
	}
	args = args[1:]
	fs := flag.NewFlagSet("sessions "+verb, flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	root := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
	format := fs.String("format", "text", "output format: text or json")
	var cwd, originArg, title, tags, archiveArg string
	var yes bool
	switch verb {
	case "list":
		fs.StringVar(&cwd, "cwd", "", "only list sessions saved with this cwd (absolute)")
		fs.StringVar(&originArg, "origin", "", "only list sessions of one surface: local, gateway or print; empty includes all three")
	case "show":
	case "update":
		fs.StringVar(&title, "title", "", "rename the session")
		fs.StringVar(&tags, "tags", "", "replace tags with a comma-separated list (empty clears all tags)")
		fs.StringVar(&archiveArg, "archived", "", "set archive state with --archived=true or --archived=false")
	case "delete":
		fs.BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	case "create":
		return errors.New(`there is no sessions create command; start coddy and send a prompt, or run coddy -p "your prompt"`)
	default:
		return fmt.Errorf("unknown sessions subcommand %q (try %s sessions --help)", verb, os.Args[0])
	}
	fs.Usage = func() {
		id := ""
		if verb != "list" {
			id = " <id>"
		}
		_, _ = fmt.Fprintf(fs.Output(), "usage: %s sessions %s%s [flags]\n", os.Args[0], verb, id)
		fs.PrintDefaults()
		if verb == "update" || verb == "delete" {
			_, _ = fmt.Fprintln(fs.Output(), sessionsMutationHelp)
		}
	}
	var id string
	if verb != "list" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id = strings.TrimSpace(args[0])
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || (verb != "list" && id == "") {
		fs.Usage()
		if verb == "list" {
			return errors.New("sessions list does not take positional arguments")
		}
		return fmt.Errorf("sessions %s requires one session ID or prefix before its flags", verb)
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("--format must be text or json, not %q", *format)
	}
	origin, ok := session.ParseOriginFilter(originArg)
	if !ok {
		return fmt.Errorf("--origin must be local, gateway or print, not %q", originArg)
	}
	patch := map[string]any{}
	if verb == "update" {
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "title":
				patch["title"] = session.NormalizeTitle(title)
			case "tags":
				patch["tags"] = append([]string{}, session.ParseTagList(tags)...)
			case "archived":
				patch["archived"] = archiveArg == "true"
			}
		})
		if _, set := patch["archived"]; set && archiveArg != "true" && archiveArg != "false" {
			return errors.New("--archived must be true or false")
		}
		if len(patch) == 0 {
			return errors.New("sessions update requires --title, --tags or --archived")
		}
		if v, ok := patch["title"].(string); ok {
			if v == "" {
				return errors.New("--title must not be blank")
			}
			if _, long := session.TitleTooLong(v); long {
				return fmt.Errorf("--title must be at most %d characters", session.MaxSessionTitleRunes)
			}
		}
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	store, err := openSessionStore(*root, cfg)
	if err != nil {
		return err
	}
	opts := sessionCLIListOptions(cwd, origin)
	if verb == "list" && *format == "text" {
		return sessionsList(c.out, store, cwd, origin)
	}
	if verb != "list" {
		opts.Archived = session.ArchiveAll
	}
	rows, err := store.ListSnapshotsWith(opts)
	if err != nil {
		return err
	}
	if verb == "list" {
		result := make([]storedSessionInfo, 0, len(rows))
		for _, row := range rows {
			result = append(result, storedSessionRow(row))
		}
		return json.NewEncoder(c.out).Encode(result)
	}
	row, err := resolveVisibleSession(rows, id)
	if err != nil {
		return err
	}
	if verb == "show" {
		return c.show(store, row, *format == "json")
	}
	method := http.MethodPatch
	if verb == "delete" {
		if current := strings.TrimSpace(os.Getenv("CODDY_SESSION_ID")); current != "" && sessionContainsCurrent(store, row.SessionID, current) {
			return fmt.Errorf("cannot delete session %q: it is or contains the current session %q (CODDY_SESSION_ID)", row.SessionID, current)
		}
		if !yes {
			if err := c.confirmDelete(row); err != nil {
				return err
			}
		}
		method, patch = http.MethodDelete, nil
	}
	result, err := c.mutate(context.Background(), cfg.Paths.Home, store.Root, row.SessionID, method, patch)
	if err != nil {
		return fmt.Errorf("%s session %q: %w", verb, row.SessionID, err)
	}
	if *format == "json" {
		return json.NewEncoder(c.out).Encode(result)
	}
	action := "Updated"
	if verb == "delete" {
		action = "Deleted"
	}
	_, err = fmt.Fprintf(c.out, "%s session %s\n", action, row.SessionID)
	return err
}

func (c sessionsCommand) confirmDelete(row session.SessionListEntry) error {
	if !c.interactive {
		return errors.New("sessions delete requires an interactive terminal or --yes")
	}
	if _, err := fmt.Fprintf(c.errOut, "Delete session %s (%q) and all its descendants? [y/N] ", row.SessionID, row.Title); err != nil {
		return err
	}
	answer, err := bufio.NewReader(c.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read delete confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return errors.New("session deletion cancelled")
	}
}

func sessionContainsCurrent(store *session.FileStore, id, current string) bool {
	if id == current {
		return true
	}
	if session.ValidateFolderSessionID(current) != nil {
		return false
	}
	target, child := store.SessionPath(id), store.SessionPath(current)
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	if real, err := filepath.EvalSymlinks(child); err == nil {
		child = real
	}
	rel, err := filepath.Rel(target, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveVisibleSession(rows []session.SessionListEntry, id string) (session.SessionListEntry, error) {
	var hits []string
	var found session.SessionListEntry
	for _, row := range rows {
		if row.SessionID == id {
			return row, nil
		}
		if strings.HasPrefix(row.SessionID, id) {
			found = row
			hits = append(hits, row.SessionID)
		}
	}
	switch len(hits) {
	case 0:
		return found, fmt.Errorf("no visible session matches %q", id)
	case 1:
		return found, nil
	default:
		sort.Strings(hits)
		return session.SessionListEntry{}, fmt.Errorf("ambiguous session ID prefix %q: %s", id, strings.Join(hits, ", "))
	}
}

// Match the existing list scope: local/print/gateway top-level sessions, not
// child or scheduler runs. Direct verbs also look on the archived side.
func sessionCLIListOptions(cwd string, origin session.OriginFilter) session.ListOptions {
	return session.ListOptions{CWD: strings.TrimSpace(cwd), Origin: origin, IncludePrintRuns: true}
}

func sessionsList(w io.Writer, store *session.FileStore, cwd string, origin session.OriginFilter) error {
	rows, err := store.ListSnapshotsWith(sessionCLIListOptions(cwd, origin))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "SESSION_ID\tUPDATED_AT\tCWD\tTITLE"); err != nil {
		return err
	}
	for _, r := range rows {
		title := strings.NewReplacer("\t", " ", "\n", " ").Replace(r.Title)
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.SessionID, r.UpdatedAt, r.CWD, title); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "(total %d)\n", len(rows))
	return err
}

type storedSessionInfo struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	CWD          string   `json:"cwd"`
	Mode         string   `json:"mode,omitempty"`
	Model        string   `json:"model,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	Tags         []string `json:"tags"`
	Archived     bool     `json:"archived"`
	ArchivedAt   string   `json:"archivedAt,omitempty"`
	CreatedAt    string   `json:"createdAt,omitempty"`
	UpdatedAt    string   `json:"updatedAt,omitempty"`
	MessageCount *int     `json:"messageCount,omitempty"`
	TaskCount    *int     `json:"taskCount,omitempty"`
}

func storedSessionRow(r session.SessionListEntry) storedSessionInfo {
	return storedSessionInfo{
		ID: r.SessionID, Title: r.Title, CWD: r.CWD, Model: r.Model, Origin: r.Origin,
		Tags: append([]string{}, r.Tags...), Archived: r.Archived, ArchivedAt: r.ArchivedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (c sessionsCommand) show(store *session.FileStore, row session.SessionListEntry, asJSON bool) error {
	snap, err := store.ReadSnapshot(row.SessionID)
	if err != nil {
		return fmt.Errorf("read session %q: %w", row.SessionID, err)
	}
	info := storedSessionRow(row)
	info.Mode = snap.Meta.Mode
	count := len(snap.Messages)
	info.MessageCount = &count
	taskCount := len(bgtask.LoadPersisted(snap.Dir))
	info.TaskCount = &taskCount
	if asJSON {
		return json.NewEncoder(c.out).Encode(info)
	}
	_, err = fmt.Fprintf(c.out, "ID: %s\nTitle: %s\nCWD: %s\nMode: %s\nModel: %s\nTags: %s\nArchived: %t\nArchived at: %s\nCreated at: %s\nUpdated at: %s\nMessages: %d\nBackground tasks (recorded): %d\n",
		info.ID, info.Title, info.CWD, info.Mode, info.Model, strings.Join(info.Tags, ", "), info.Archived, info.ArchivedAt, info.CreatedAt, info.UpdatedAt, count, taskCount)
	return err
}
