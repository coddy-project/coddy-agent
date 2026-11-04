package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
	"rsc.io/qr"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// totpConfirmAttempts is how many codes the operator may type before the
// command gives up and writes nothing: enough for a slip of the thumb and a
// code that changed while it was being typed.
const totpConfirmAttempts = 3

// Seams for the tests: the secret a run generates and the clock it checks the
// confirming code against.
var (
	newTOTPSecret = webauth.GenerateTOTPSecret
	totpNow       = time.Now
)

// runServeSetTOTP implements `coddy serve set-totp`: it turns on one-time codes
// at the web UI sign-in, or turns them off with --disable.
//
// The secret is shown once, as a QR code an authenticator app scans and as
// text for an app that is typed into, and it is written into config.yaml only
// after the app has answered with a code that checks. A secret written before
// that would lock the operator out of their own form the moment the scan went
// wrong. Turning the codes off is the way back in for an operator who lost the
// phone: it needs a shell on the machine, which is the point.
func runServeSetTOTP(args []string) error {
	fs := flag.NewFlagSet("serve set-totp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	disable := fs.Bool("disable", false, "turn one-time codes off: the password alone signs in again")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(),
			"Usage of serve set-totp (turn on one-time codes from an authenticator app at the web UI sign-in):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cli := config.CLIPaths{Home: strings.TrimSpace(*homeDir), Config: strings.TrimSpace(*cfgPath)}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		return fmt.Errorf("read %s: %w", paths.ConfigPath, err)
	}

	if *disable {
		if !cfg.HTTPServer.Login.HasTOTP() {
			fmt.Printf("one-time codes are already off\n  config    %s\n", paths.ConfigPath)
			return nil
		}
		result, err := config.CommitUCICommands(paths, []config.UCICommand{
			{Op: config.UCIOpDelete, Path: "httpserver.login.totp_secret"},
		})
		if err != nil {
			return fmt.Errorf("write %s: %w", paths.ConfigPath, err)
		}
		fmt.Printf("one-time codes are off: the password alone signs in\n  config    %s\n  previous  %s\n",
			paths.ConfigPath, result.SnapshotPath)
		fmt.Printf("  every browser signed in with a code signs in again\n")
		fmt.Printf("  remove the \"%s\" entry from the authenticator app\n", webauth.TOTPIssuer)
		return nil
	}

	account := totpAccountName(cfg)
	if account == "" {
		return fmt.Errorf("no web UI sign-in account is configured: one-time codes are asked for after the password, "+
			"so set one first with `coddy serve set-password` (or %s / %s)",
			httpserver.LoginUserEnvVar, httpserver.LoginPasswordEnvVar)
	}

	secret, err := newTOTPSecret()
	if err != nil {
		return err
	}
	uri := webauth.TOTPKeyURI(account, secret)
	if err := printTOTPEnrolment(os.Stdout, term.IsTerminal(int(os.Stdout.Fd())), account, secret, uri); err != nil {
		return err
	}
	if err := confirmTOTP(os.Stdin, os.Stderr, secret); err != nil {
		return err
	}

	// The secret is base32: no "$" for the environment expansion to read, so
	// it is written as it is.
	result, err := config.CommitUCICommands(paths, []config.UCICommand{
		{Op: config.UCIOpSet, Path: "httpserver.login.totp_secret", Value: secret},
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", paths.ConfigPath, err)
	}
	fmt.Printf("one-time codes are on for %q\n  config    %s\n  previous  %s\n", account, paths.ConfigPath, result.SnapshotPath)
	fmt.Printf("  the sign-in form now asks for the code after the password; browsers\n" +
		"  signed in before sign in again. `coddy serve set-totp --disable` turns it off.\n")
	if cfg.HTTPServer.Login.IsExplicitlyDisabled() {
		fmt.Printf("note: httpserver.login.enable is false, so the form is off and no code is asked for yet.\n")
	}
	fmt.Printf("note: the code guards the browser sign-in only. API clients present a bearer\n" +
		"      token (httpserver.auth_token, --auth-token or " + httpserver.TokenEnvVar + ") and are not asked for one.\n")
	return nil
}

// totpAccountName is the account the sign-in form checks: the environment's
// when it has one, since that one wins at the server too, else the file's.
func totpAccountName(cfg *config.Config) string {
	if user := strings.TrimSpace(os.Getenv(httpserver.LoginUserEnvVar)); user != "" &&
		os.Getenv(httpserver.LoginPasswordEnvVar) != "" {
		return user
	}
	if cfg.HTTPServer.Login.HasAccount() {
		return cfg.HTTPServer.Login.User
	}
	return ""
}

// printTOTPEnrolment shows what an authenticator app needs: the QR code, and
// for an app that is typed into, the secret in groups of four.
func printTOTPEnrolment(out io.Writer, colour bool, account, secret, uri string) error {
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return fmt.Errorf("draw the QR code: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Scan this QR code with an authenticator app (Google Authenticator, FreeOTP,\n"+
		"Aegis, a password manager). It adds %q under the name %q.\n\n", account, webauth.TOTPIssuer)
	_, _ = io.WriteString(out, renderQR(code, colour))
	_, _ = fmt.Fprintf(out, "\nOr type the key into the app by hand (time-based, 6 digits, 30 seconds):\n  %s\n", groupSecret(secret))
	_, _ = fmt.Fprintf(out, "\nThe key is shown this once. Anybody who sees it can make codes for this server.\n\n")
	return nil
}

// qrQuietZone is the light border a scanner needs to find the code: four
// modules in the standard, two are plenty on a screen.
const qrQuietZone = 2

// renderQR draws the code with half blocks, two rows of modules per line of
// text, so it is square on a terminal and fits in a narrow one.
//
// On a terminal the colours are set explicitly - dark modules black on white -
// because the same characters would be drawn inverted on a dark theme, and
// not every app reads an inverted code. Into a file or a pipe it writes plain
// characters, dark modules as ink.
func renderQR(code *qr.Code, colour bool) string {
	dark := func(x, y int) bool {
		x -= qrQuietZone
		y -= qrQuietZone
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	side := code.Size + 2*qrQuietZone
	var b strings.Builder
	for y := 0; y < side; y += 2 {
		if colour {
			// Foreground paints the upper module, background the lower one.
			for x := 0; x < side; x++ {
				fg, bg := "97", "107"
				if dark(x, y) {
					fg = "30"
				}
				if y+1 < side && dark(x, y+1) {
					bg = "40"
				}
				b.WriteString("\x1b[" + fg + ";" + bg + "m▀")
			}
			b.WriteString("\x1b[0m\n")
			continue
		}
		for x := 0; x < side; x++ {
			top, bottom := dark(x, y), y+1 < side && dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// groupSecret spells a secret in groups of four, the way apps print one.
func groupSecret(secret string) string {
	var parts []string
	for len(secret) > 4 {
		parts = append(parts, secret[:4])
		secret = secret[4:]
	}
	if secret != "" {
		parts = append(parts, secret)
	}
	return strings.Join(parts, " ")
}

// confirmTOTP asks for the code the app shows and checks it, so a secret that
// did not reach the app is never written. It reads lines, which works on a
// terminal and from a pipe alike.
func confirmTOTP(in io.Reader, out io.Writer, secret string) error {
	reader := bufio.NewReader(in)
	for attempt := 1; attempt <= totpConfirmAttempts; attempt++ {
		_, _ = fmt.Fprint(out, "Code from the app: ")
		line, err := reader.ReadString('\n')
		if err == io.EOF && strings.TrimSpace(line) == "" {
			_, _ = fmt.Fprintln(out)
			return errors.New("no code was typed, nothing was written; run the command again and scan the new code")
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("read the code: %w; nothing was written", err)
		}
		if _, ok := webauth.VerifyTOTP(secret, line, totpNow()); ok {
			return nil
		}
		if err == io.EOF {
			break
		}
		if attempt < totpConfirmAttempts {
			_, _ = fmt.Fprintln(out, "That code does not match. Check the phone's clock and type the code shown now.")
		}
	}
	return errors.New("the app did not confirm the key, nothing was written; run the command again and scan the new code")
}
