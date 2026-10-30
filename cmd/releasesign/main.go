// Command releasesign signs what a release publishes (docs/contributing/build.md,
// Signing releases).
//
//	releasesign keygen <secret file>   make a key pair: the secret goes to the file, the public key to stdout
//	releasesign sign <tag> <file>      sign file with RELEASE_SIGNING_KEY, writing <file>.minisig
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/minisig"
	"github.com/EvilFreelancer/coddy-agent/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 2 && args[0] == "keygen":
		return keygen(args[1])
	case len(args) == 3 && args[0] == "sign":
		return sign(args[1], args[2], os.Getenv("RELEASE_SIGNING_KEY"))
	}
	return fmt.Errorf("usage: releasesign keygen <secret file> | releasesign sign <tag> <file>")
}

func keygen(path string) error {
	secret, pub, err := minisig.GenerateKey()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, secret); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println(pub)
	return nil
}

func sign(tag, path, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("RELEASE_SIGNING_KEY is not set")
	}
	message, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	signature, err := minisig.Sign(secret, message, minisig.ReleaseComment(tag))
	if err != nil {
		return err
	}
	if err := update.VerifyReleaseSignature(tag, message, signature); err != nil {
		return fmt.Errorf("verify the release signing key: %w", err)
	}
	return os.WriteFile(path+".minisig", signature, 0o644)
}
