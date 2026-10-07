//go:build gateway || gateway.pachca

// Package pachca implements the Pachca (Пачка) bot adapter for the Coddy
// gateway: an integration bot of a Pachca workspace that reads its events
// history, runs a turn for every message addressed to it, and streams the
// answer into one message of the chat.
package pachca

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/proxyutil"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	adapterName    = "pachca"
	workerQueueCap = 32 // max queued messages per session
)

// SessionRunner is what the bot needs from the session manager;
// session.Manager satisfies it.
type SessionRunner interface {
	EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*session.State, error)
	HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
	ForgetLiveSession(sessionID string)
	HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
	Cfg() *config.Config
}

// PromptSurfaces is where the bot offers to ask about a detached subagent of
// one of its chats; `coddy serve` passes its runtime.
type PromptSurfaces interface {
	AddDetachedPermissionApprover(agent.DetachedPermissionBroker) (withdraw func())
}

// Bot is the Pachca gateway adapter.
type Bot struct {
	cfg    *config.PachcaGatewayConfig
	runner SessionRunner
	cwd    string
	log    *slog.Logger
	store  *sessionstore.Store
	state  *pollState
	mirror session.TurnMirror

	// apiBase is the API root Start connects to; empty means the
	// CODDY_PACHCA_API_BASE environment variable, and failing that
	// config.DefaultPachcaAPIBase. Tests set it to point at a fake.
	apiBase string
	// pollEvery, when set, replaces the configured poll interval; tests poll
	// faster than the one-second floor of the configuration.
	pollEvery time.Duration
	// emptyLogWarnAfter is how long an events history may stay empty after
	// the start before the bot says it may be switched off.
	emptyLogWarnAfter time.Duration

	// Who the bot is in the workspace, read once at Start.
	selfID      int64
	nickname    string
	displayName string

	mu      sync.Mutex
	workers map[string]chan workerJob
	// stopping is set once intake ends: an event that arrives after it is
	// left in the history for the next process.
	stopping bool

	inFlight sync.WaitGroup

	apiMu  sync.Mutex
	client *Client

	chats   *chatKinds
	posted  *idSet // messages the bot itself posted
	parents *parentCache
	seen    *seenEvents

	asks           *chatPermissions
	promptSurfaces PromptSurfaces
	wakeSurfaces   agent.WakeSurfaces
}

type workerJob struct {
	in  inbound
	key string
}

// New creates a Bot. storePath persists the chat-to-session map and statePath
// the position in the events history; "" keeps either in memory only.
func New(cfg *config.PachcaGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger, storePath, statePath string, mirror session.TurnMirror) *Bot {
	if mirror == nil {
		mirror = session.NopTurnMirror{}
	}
	return &Bot{
		cfg:               cfg,
		runner:            runner,
		cwd:               cwd,
		log:               log,
		store:             sessionstore.NewPersisted(storePath),
		state:             loadPollState(statePath),
		mirror:            mirror,
		emptyLogWarnAfter: 5 * time.Minute,
		workers:           make(map[string]chan workerJob),
		chats:             newChatKinds(),
		posted:            newIDSet(4096),
		parents:           newParentCache(4096),
		seen:              newSeenEvents(4096),
		asks:              newChatPermissions(),
	}
}

// Name satisfies gateway.Adapter.
func (b *Bot) Name() string { return "pachca" }

// SetPromptSurfaces names where the bot offers to ask about detached
// subagents of its chats. Nil (the default) means such a prompt never reaches
// a chat.
func (b *Bot) SetPromptSurfaces(p PromptSurfaces) { b.promptSurfaces = p }

// SetWakeSurfaces names where the bot offers to run the woken turns of its
// chats' sessions. Nil (the default) means a woken turn never reaches a chat.
func (b *Bot) SetWakeSurfaces(w agent.WakeSurfaces) { b.wakeSurfaces = w }

// apiBaseURL is the API root this bot talks to.
func (b *Bot) apiBaseURL() string {
	base := b.apiBase
	if base == "" {
		base = os.Getenv(config.PachcaAPIBaseEnv)
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return config.DefaultPachcaAPIBase
	}
	return base
}

// Start connects to Pachca and reads the events history until ctx is
// cancelled.
func (b *Bot) Start(ctx context.Context) error {
	hc, err := proxyutil.BuildHTTPClient(b.cfg.Proxy)
	if err != nil {
		return fmt.Errorf("pachca: %w", err)
	}
	token := b.cfg.EffectiveToken()
	if token == "" {
		return fmt.Errorf("pachca: no bot token; set gateways.pachca.token or the %s environment variable", config.PachcaBotTokenEnvVar)
	}
	base := b.apiBaseURL()
	if base != config.DefaultPachcaAPIBase {
		b.log.Info("pachca: api base override", "base", base)
	}
	client := NewClient(base, token, hc)

	info, err := client.TokenInfo(ctx)
	if err != nil {
		return fmt.Errorf("pachca: token check: %w", err)
	}
	profile, err := client.Profile(ctx)
	if err != nil {
		return fmt.Errorf("pachca: read the bot's profile (scope profile:read): %w", err)
	}
	b.selfID = info.UserID
	if b.selfID == 0 {
		b.selfID = profile.ID
	}
	b.nickname = strings.TrimPrefix(strings.TrimSpace(profile.Nickname), "@")
	b.displayName = profile.DisplayName()
	b.log.Info("pachca bot connected", "nickname", b.nickname, "user_id", b.selfID)
	if missing := missingScopes(info.Scopes, requiredScopes); len(missing) > 0 {
		b.log.Warn("pachca: the bot token lacks scopes it needs", "missing", strings.Join(missing, ","))
	}

	// The hub starts this bot again after an error: workers of the previous
	// run ended with its context, and its permission prompts were withdrawn,
	// so both start afresh.
	b.mu.Lock()
	b.stopping = false
	b.workers = make(map[string]chan workerJob)
	b.mu.Unlock()
	asks := newChatPermissions()
	b.apiMu.Lock()
	b.asks = asks
	b.apiMu.Unlock()
	b.setClient(client)
	defer b.setClient(nil)
	defer asks.stop()
	if b.promptSurfaces != nil {
		withdraw := b.promptSurfaces.AddDetachedPermissionApprover(b)
		defer withdraw()
	}
	if b.wakeSurfaces != nil {
		withdraw := b.wakeSurfaces.AddWakeSurface(b, agent.WakeOwner)
		defer withdraw()
	}

	// Turns run under a context of their own, so that stopping the bot stops
	// intake first and generation second: a settings change that rotates the
	// token must not cut an answer off half-written.
	turnCtx, cancelTurns := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTurns()

	err = b.pollLoop(ctx, turnCtx, client)

	b.mu.Lock()
	b.stopping = true
	b.mu.Unlock()
	b.drain()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// requiredScopes are the token scopes the bot cannot work without.
var requiredScopes = []string{
	"messages:create", "messages:update", "messages:read",
	"chats:read", "profile:read", "webhooks:events:read",
}

func missingScopes(have, want []string) []string {
	set := make(map[string]bool, len(have))
	for _, s := range have {
		set[s] = true
	}
	var out []string
	for _, s := range want {
		if !set[s] {
			out = append(out, s)
		}
	}
	return out
}

func (b *Bot) setClient(c *Client) {
	b.apiMu.Lock()
	b.client = c
	b.apiMu.Unlock()
}

// permissions is the prompt registry of the current run.
func (b *Bot) permissions() *chatPermissions {
	b.apiMu.Lock()
	defer b.apiMu.Unlock()
	return b.asks
}

// connectedClient is the client of a running bot, nil when stopped.
func (b *Bot) connectedClient() *Client {
	b.apiMu.Lock()
	defer b.apiMu.Unlock()
	return b.client
}

// drainTimeout bounds the wait for turns still running when the bot stops.
const drainTimeout = 20 * time.Second

func (b *Bot) drain() {
	done := make(chan struct{})
	go func() {
		b.inFlight.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		b.log.Warn("pachca: turns still running at stop", "waited", drainTimeout)
	}
}

// enqueue hands a message to the worker of its session key. It reports false
// when the bot is stopping: the event then stays in the history. A queued
// message counts as in flight from here, so a stop waits for it as well as
// for the turn running ahead of it: its event is already gone.
func (b *Bot) enqueue(ctx context.Context, in inbound, key string) (accepted, queued bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		return false, false
	}
	ch, ok := b.workers[key]
	if !ok {
		ch = make(chan workerJob, workerQueueCap)
		b.workers[key] = ch
		go b.sessionWorker(ctx, ch)
	}
	b.inFlight.Add(1)
	select {
	case ch <- workerJob{in: in, key: key}:
		return true, true
	default:
		b.inFlight.Done()
		return true, false
	}
}

// beginTurn counts a turn that did not come through a queue (a woken one) as
// in flight, unless the bot is stopping.
func (b *Bot) beginTurn() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		return false
	}
	b.inFlight.Add(1)
	return true
}

func (b *Bot) sessionWorker(ctx context.Context, ch chan workerJob) {
	for {
		select {
		case job := <-ch:
			b.processMessage(ctx, job.in, job.key)
			b.inFlight.Done()
		case <-ctx.Done():
			// The stop waited as long as it could: what is still queued
			// is let go, and no longer counted.
			for {
				select {
				case <-ch:
					b.inFlight.Done()
				default:
					return
				}
			}
		}
	}
}

// targetForKey is where a conversation's messages go. A direct conversation
// is keyed by the person, and Pachca's direct chat ids are not user ids, so it
// is addressed through the person; anything else through the chat.
func targetForKey(key string) (Target, bool) {
	parts := strings.Split(key, ":")
	id, ok := sessionstore.ChatID(key)
	if !ok || len(parts) < 2 {
		return Target{}, false
	}
	if parts[1] == "user" {
		return UserTarget(id), true
	}
	return ChatTarget(id), true
}

// reply posts a short service message into target, as a reply to parent when
// that is not zero.
func (b *Bot) reply(ctx context.Context, c *Client, target Target, parent int64, text string) {
	if c == nil {
		return
	}
	m, err := c.SendMessage(ctx, OutgoingMessage{Target: target, Content: text, ParentID: parent})
	if err != nil {
		b.log.Warn("pachca: reply not delivered", "err", err, "target", target.EntityID)
		return
	}
	b.posted.add(m.ID)
}
