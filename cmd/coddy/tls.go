package main

// `coddy tls ...` is the operator's view of the built-in certificate authority (internal/pki): the certificates TLS needs, made with
// the standard library at installation, at update and on demand under $CODDY_HOME/tls. TLS is the transport's business; nothing here
// decides what the holder of a certificate may do (docs/plans/remote-model-provider-tls-builtin.md). The domain logic lives in
// internal/pki; this file wires it to flags, the configuration's addresses and stdout.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/pki"
)

func tlsUsage() string {
	return fmt.Sprintf("usage: %[1]s tls ensure [--quiet] [--force-ca] | status [--json] | renew | export | trust <file|-> | issue client <name> [-o DIR]"+
		" (flags: --home DIR, --config PATH, --name HOST, repeatable, extra names for the server certificate)", os.Args[0])
}

// tlsFlags are the flags every `coddy tls` verb takes.
type tlsFlags struct {
	home, config string
	names        stringList
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func (f *tlsFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.home, "home", "", "agent state directory (CODDY_HOME, default ~/.coddy); the certificates live in <home>/tls")
	fs.StringVar(&f.config, "config", "", "path to config.yaml, read for the addresses the server certificate must carry")
	fs.Var(&f.names, "name", "an extra DNS name or IP address for the server certificate (repeatable)")
}

// dir is the certificate directory and the configuration that names this machine's addresses. A configuration that cannot be read does
// not stop the authority: the certificates are needed to start, and a broken file is `coddy -t`'s to report.
func (f *tlsFlags) dir(errw io.Writer) (string, *config.Config, error) {
	cli := config.CLIPaths{Home: strings.TrimSpace(f.home), Config: strings.TrimSpace(f.config)}
	paths, err := config.Resolve(cli)
	if err != nil {
		return "", nil, err
	}
	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "coddy tls: the configuration was not read (%v); the names come from the host and the flags only\n", err)
		cfg = nil
	}
	return pki.Dir(paths.Home), cfg, nil
}

// tlsWant is what the configuration and the flags ask the authority for.
func tlsWant(cfg *config.Config, extra []string) pki.Want {
	host := pki.Hostname()
	var hosts []string
	if cfg != nil {
		hosts = cfg.TLSHosts()
	}
	return pki.Want{Host: host, Names: pki.DefaultNames(host, nil, hosts, extra)}
}

func runTLS(args []string, out, errw io.Writer, in io.Reader) error {
	if len(args) < 1 {
		return errors.New(tlsUsage())
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("tls "+verb, flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.Usage = func() { _, _ = fmt.Fprintln(errw, tlsUsage()) }
	var f tlsFlags
	f.register(fs)

	// A positional argument comes before the flags (`trust FILE --home DIR`, `issue client NAME -o DIR`).
	var positional []string
	for len(rest) > 0 && (!strings.HasPrefix(rest[0], "-") || rest[0] == "-") {
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	quiet := fs.Bool("quiet", false, "ensure: print nothing unless something failed")
	forceCA := fs.Bool("force-ca", false, "ensure: make a new CA even when this one is valid (every peer must then trust the new CA)")
	asJSON := fs.Bool("json", false, "status: print JSON")
	outDir := fs.String("o", "", "issue: the directory for the files (default: the current directory)")
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	// Flags may also follow the positional arguments of a verb that has more than one (`issue client NAME -o DIR`): Parse stops at the
	// first non-flag, so what is left over is positional too.
	positional = append(positional, fs.Args()...)

	dir, cfg, err := f.dir(errw)
	if err != nil {
		return err
	}
	now := time.Now()

	switch verb {
	case "ensure":
		return tlsEnsure(out, dir, tlsWant(cfg, f.names), *forceCA, false, *quiet, now)
	case "renew":
		return tlsEnsure(out, dir, tlsWant(cfg, f.names), false, true, *quiet, now)
	case "status":
		return tlsStatus(out, dir, tlsWant(cfg, f.names), *asJSON, now)
	case "export":
		pemData, err := pki.ExportCA(dir)
		if err != nil {
			return err
		}
		_, err = out.Write(pemData)
		return err
	case "trust":
		if len(positional) != 1 {
			return errors.New("usage: " + os.Args[0] + " tls trust <file|->")
		}
		return tlsTrust(out, in, dir, positional[0], now)
	case "issue":
		if len(positional) != 2 || positional[0] != "client" {
			return errors.New("usage: " + os.Args[0] + " tls issue client <name> [-o DIR]")
		}
		return tlsIssueClient(out, dir, positional[1], *outDir, now)
	default:
		return fmt.Errorf("unknown tls subcommand %q (%s)", verb, tlsUsage())
	}
}

func tlsEnsure(out io.Writer, dir string, w pki.Want, forceCA, renew, quiet bool, now time.Time) error {
	w.ForceCA, w.RenewLeaves = forceCA, renew
	res, err := pki.Ensure(dir, w, func() time.Time { return now })
	if err != nil {
		return err
	}
	if quiet {
		return nil
	}
	if !res.Changed() {
		_, _ = fmt.Fprintf(out, "%s: nothing to do\n", dir)
		return nil
	}
	for _, s := range res.Steps {
		_, _ = fmt.Fprintf(out, "%s: %s\n", s.Action, s.Reason)
	}
	if res.NewCA {
		_, _ = fmt.Fprintf(out, "\nThe CA is new: every peer that trusted the old one must trust this one (%s tls export, then %s tls trust on the peer).\n", os.Args[0], os.Args[0])
	}
	return nil
}

func tlsStatus(out io.Writer, dir string, w pki.Want, asJSON bool, now time.Time) error {
	s := pki.Describe(dir, w, now)
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Directory:\t%s\n", s.Dir)
	row := func(label string, c pki.CertStatus, keyOK bool) {
		switch {
		case !c.Present:
			_, _ = fmt.Fprintf(tw, "%s:\tmissing\n", label)
		default:
			note := ""
			if !keyOK {
				note = "  (the key file is wrong)"
			}
			_, _ = fmt.Fprintf(tw, "%s:\tends %s (%d days)%s\t%s\n", label, c.NotAfter.UTC().Format("2006-01-02"), c.DaysLeft, note, strings.Join(c.Names, " "))
		}
	}
	row("CA", s.CA, s.CAKeyOK)
	row("Server certificate", s.Server, s.Server.KeyMatches)
	row("Client certificate", s.Client, s.Client.KeyMatches)
	for _, t := range s.Trusted {
		_, _ = fmt.Fprintf(tw, "Trusted CA:\t%s\tends %s\n", t.Fingerprint[:16], t.NotAfter.UTC().Format("2006-01-02"))
	}
	_, _ = fmt.Fprintf(tw, "Bundle:\t%s\n", map[bool]string{true: "bundle.pem is current", false: "bundle.pem needs rebuilding"}[s.BundleOK])
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, f := range s.Findings(w.RenewBefore) {
		_, _ = fmt.Fprintf(out, "warning: %s\n", f)
	}
	for _, p := range s.Pending {
		_, _ = fmt.Fprintf(out, "pending: %s (%s)\n", p.Action, p.Reason)
	}
	return nil
}

func tlsTrust(out io.Writer, in io.Reader, dir, src string, now time.Time) error {
	var raw []byte
	var err error
	if src == "-" {
		raw, err = io.ReadAll(io.LimitReader(in, 1<<20))
	} else {
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return err
	}
	added, fp, err := pki.Trust(dir, raw, now)
	if err != nil {
		return err
	}
	if added {
		_, _ = fmt.Fprintf(out, "trusted CA %s; a running server reads it at its next start\n", fp[:16])
	} else {
		_, _ = fmt.Fprintf(out, "CA %s was already trusted\n", fp[:16])
	}
	return nil
}

func tlsIssueClient(out io.Writer, dir, name, outDir string, now time.Time) error {
	if outDir == "" {
		outDir = "."
	}
	certPath, keyPath, caPath, err := pki.IssueClientFiles(dir, name, outDir, now, 0)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "certificate: %s\nkey:         %s (keep it private)\nCA to trust: %s\n", certPath, keyPath, caPath)
	return nil
}
