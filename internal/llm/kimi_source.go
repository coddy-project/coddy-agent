package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// WithCredential runs a read operation and recovers at most one rejected
// subscription credential. The returned token is the one used on the last call.
func (s *KimiSubscriptionSource) WithCredential(ctx context.Context, call func(string) error) (string, KimiSourceKind, error) {
	ticket, err := s.ticket(ctx)
	if err != nil {
		return "", "", err
	}
	err = call(ticket.token)
	status := UpstreamStatus(err)
	var usageErr *ProviderUsageError
	if errors.As(err, &usageErr) {
		status = usageErr.Status
	}
	if status != http.StatusUnauthorized {
		return ticket.token, ticket.kind, err
	}
	token, recoveryErr := s.rejectedTicket(ctx, ticket)
	if recoveryErr != nil {
		if errors.Is(recoveryErr, ErrKimiCLILoginRequired) {
			return "", ticket.kind, fmt.Errorf("%w: %w", err, ErrKimiCLILoginRequired)
		}
		return "", ticket.kind, recoveryErr
	}
	return token, ticket.kind, call(token)
}

// KimiSourceKind identifies the credential used for a particular request.
type KimiSourceKind string

const (
	KimiSourceManaged KimiSourceKind = "managed"
	KimiSourceCLI     KimiSourceKind = "cli"
)

// KimiSubscriptionSource selects the managed identity first and reads the CLI
// only when that identity is absent. It never writes CLI credentials.
type KimiSubscriptionSource struct {
	refresher *KimiCredentialRefresher
	region    string
	noCLI     bool
}

type kimiCredentialTicket struct {
	token      string
	kind       KimiSourceKind
	credential KimiCredential
	region     string
}

func (s *KimiSubscriptionSource) catalogTicket(ctx context.Context, call func(string) error) (kimiCredentialTicket, error) {
	ticket, err := s.ticket(ctx)
	if err != nil {
		return kimiCredentialTicket{}, err
	}
	err = call(ticket.token)
	if UpstreamStatus(err) != http.StatusUnauthorized {
		return ticket, err
	}
	token, recoveryErr := s.rejectedTicket(ctx, ticket)
	if recoveryErr != nil {
		return kimiCredentialTicket{}, recoveryErr
	}
	cred, state, loadErr := s.refresher.store.Load(s.region)
	if loadErr != nil {
		return kimiCredentialTicket{}, loadErr
	}
	if state != KimiCredentialValid || cred.AccessToken != token {
		return kimiCredentialTicket{}, errors.New("kimi: credential changed during catalog recovery")
	}
	ticket = kimiCredentialTicket{token: token, kind: KimiSourceManaged, credential: cred, region: s.region}
	return ticket, call(token)
}

func (s *KimiSubscriptionSource) ticket(ctx context.Context) (kimiCredentialTicket, error) {
	for i := 0; i < 3; i++ {
		token, kind, err := s.AccessToken(ctx)
		if err != nil {
			return kimiCredentialTicket{}, err
		}
		if kind == KimiSourceCLI {
			return kimiCredentialTicket{token: token, kind: kind, region: s.region}, nil
		}
		cred, state, err := s.refresher.store.Load(s.region)
		if err != nil {
			return kimiCredentialTicket{}, err
		}
		if state == KimiCredentialValid && cred.AccessToken == token {
			return kimiCredentialTicket{token: token, kind: kind, credential: cred, region: s.region}, nil
		}
	}
	return kimiCredentialTicket{}, errors.New("kimi: credential changed while preparing request")
}

func (s *KimiSubscriptionSource) rejectedTicket(ctx context.Context, ticket kimiCredentialTicket) (string, error) {
	if ticket.kind == KimiSourceCLI {
		return "", ErrKimiCLILoginRequired
	}
	if ticket.kind != KimiSourceManaged || s.refresher == nil {
		return "", errors.New("kimi: unknown credential source")
	}
	return s.refresher.lockedRefresh(ctx, ticket.credential, true)
}

// NewKimiSubscriptionSource binds a subscription identity and its proxy. Do not
// construct this source for a Platform row.
func NewKimiSubscriptionSource(store *KimiCredentialStore, mode, region, proxy string, noCLILogin bool) (*KimiSubscriptionSource, error) {
	if mode != "" && mode != "subscription" {
		return nil, errors.New("kimi: subscription credential source requires subscription mode")
	}
	if _, err := HTTPClientForProviderProxy(proxy); err != nil {
		return nil, err
	}
	region, err := KimiCanonicalRegion(region)
	if err != nil {
		return nil, err
	}
	var refresher *KimiCredentialRefresher
	if store != nil {
		if store.Dir() == "" {
			return nil, errors.New("kimi: credential directory required")
		}
		refresher, err = NewKimiCredentialRefresherForRegion(store, region, proxy)
		if err != nil {
			return nil, err
		}
	}
	return &KimiSubscriptionSource{refresher: refresher, region: region, noCLI: noCLILogin}, nil
}

// AccessToken resolves the credential immediately before a request.
func (s *KimiSubscriptionSource) AccessToken(ctx context.Context) (string, KimiSourceKind, error) {
	var token string
	err := ErrKimiCredentialMissing
	if s.refresher != nil {
		token, err = s.refresher.AccessToken(ctx)
	}
	if err == nil {
		return token, KimiSourceManaged, nil
	}
	if !errors.Is(err, ErrKimiCredentialMissing) {
		return "", "", err
	}
	token, err = ReadKimiCLIAccessToken("subscription", s.region, s.noCLI)
	if err != nil {
		return "", "", err
	}
	return token, KimiSourceCLI, nil
}

// Rejected handles a 401 for the source returned by AccessToken. A CLI token
// must be renewed by the CLI or a new Coddy sign-in, never by Coddy.
func (s *KimiSubscriptionSource) Rejected(ctx context.Context, kind KimiSourceKind) (string, error) {
	switch kind {
	case KimiSourceCLI:
		return "", ErrKimiCLILoginRequired
	case KimiSourceManaged:
		return "", errors.New("kimi: rejected managed credential requires generation-aware recovery")
	default:
		return "", errors.New("kimi: unknown credential source")
	}
}

// kimiInputSource refuses legacy name-addressed managed stores.
func kimiInputSource(in ProviderInput) (*KimiSubscriptionSource, error) {
	var store *KimiCredentialStore
	if in.KimiIdentity != "" {
		if !config.ValidKimiIdentity(in.KimiIdentity) || in.AuthPath == "" || filepath.Base(filepath.Clean(in.AuthPath)) != in.KimiIdentity || filepath.Base(filepath.Dir(filepath.Clean(in.AuthPath))) != "kimi-identities" {
			return nil, errors.New("kimi: invalid managed credential identity or path")
		}
		store = NewKimiCredentialStore(in.AuthPath)
	}
	return NewKimiSubscriptionSource(store, in.Mode, in.Region, in.ProxyURL, in.NoCLILogin)
}
