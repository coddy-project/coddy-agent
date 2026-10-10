package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// NeuralDeep decisions endpoint: POST {apiBase}/decisions asks the same
// sk- key as the rest of the hub whether a shell command is safe to run
// without a human looking at it. This file carries the transport only —
// the retry window and the rejection of unsafe commands live in
// internal/agent/decisions.go.

// Failure kinds of a decision fetch. The rate-limited and unavailable kinds
// are worth retrying; unauthorized, forbidden, refused and invalid are not.
// Refused is every other 4xx: the hub read the request and turned it down
// for a reason a retry cannot change - an empty wallet (402), a model it does
// not serve (404), a request it cannot take (400, 413, 422).
const (
	NeuralDeepDecisionUnauthorized = "unauthorized"
	NeuralDeepDecisionForbidden    = "forbidden"
	NeuralDeepDecisionRateLimited  = "rate_limited"
	NeuralDeepDecisionUnavailable  = "unavailable"
	NeuralDeepDecisionRefused      = "refused"
	NeuralDeepDecisionInvalid      = "invalid"
)

// The options of the safety question. They are descriptions, not bare
// labels: the endpoint's own docs ask for that, and the models were trained
// on described options.
const (
	NeuralDeepDecisionSafe   = "safe"
	NeuralDeepDecisionUnsafe = "unsafe"
)

// neuralDeepDecisionQuestionID names the one question the safety check asks.
const neuralDeepDecisionQuestionID = "safety"

// neuralDeepDecisionRequestTimeout bounds one HTTP read of a decision call.
const neuralDeepDecisionRequestTimeout = 5 * time.Second

// NeuralDeepDecisionError is a failed decision fetch. RetryAfter carries the
// pause the hub asked for on 429 and 503 (Retry-After as seconds or an
// HTTP-date), zero when it sent none.
type NeuralDeepDecisionError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
	Detail     string
}

func (e *NeuralDeepDecisionError) Error() string {
	msg := "neuraldeep decisions: " + e.Kind
	if e.Status > 0 {
		msg += " (HTTP " + strconv.Itoa(e.Status) + ")"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// NeuralDeepDecision is the decoded answer of the safety question: the
// option the model picked and the probability it gave every option.
// StateTruncated is the hub's report that the state was longer than the
// model reads (512 tokens on frida-decisions) and was cut, so the answer
// judged only the head of the command.
type NeuralDeepDecision struct {
	Choice         string
	Probabilities  map[string]float64
	StateTruncated bool
}

// Probability returns the probability of the named option, 0 when the answer
// carried none for it.
func (d *NeuralDeepDecision) Probability(option string) float64 {
	if d == nil {
		return 0
	}
	return d.Probabilities[option]
}

// neuralDeepDecisionRequest is the safety question as it goes on the wire.
type neuralDeepDecisionRequest struct {
	Model     string                                    `json:"model"`
	State     string                                    `json:"state"`
	Questions map[string]neuralDeepDecisionQuestionSpec `json:"questions"`
}

type neuralDeepDecisionQuestionSpec struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

// neuralDeepDecisionSafetyQuestion is the one question the command safety
// check asks. The wording is English because what it judges is a shell
// command, English by nature; frida-decisions was trained on 71% Russian and
// 29% English text and reads both, and clef-flash is the surer of the two on
// English.
func neuralDeepDecisionSafetyQuestion() neuralDeepDecisionQuestionSpec {
	return neuralDeepDecisionQuestionSpec{
		Type: "choice",
		Instructions: "An unattended coding agent is about to run this shell command with no human reviewing it. " +
			"Should the command be allowed to run automatically?",
		Criteria: map[string]string{
			NeuralDeepDecisionSafe: "A routine development command: it reads, lists or searches files, builds, tests, " +
				"formats, installs dependencies or checks state. It may change files inside the project, but nothing it " +
				"does is broadly destructive or irreversible.",
			NeuralDeepDecisionUnsafe: "A destructive or dangerous command: broad or forced deletion (rm -rf, del /s, rd /s, " +
				"shred), wiping or reformatting a disk, dropping or truncating a database, force-push or history rewrite, " +
				"killing processes broadly, loosening system security (chmod -R 777 outside the project), piping a download " +
				"straight into a shell, a fork bomb, or any effect that reaches beyond the project's rebuildable files.",
		},
	}
}

// neuralDeepDecisionState frames the command the endpoint judges: the command
// itself plus where it would run. Nothing here bounds it: the hub cuts a
// state longer than the model reads and reports the cut, which the decision
// carries as StateTruncated.
func neuralDeepDecisionState(command, cwd string) string {
	state := "command: " + strings.TrimSpace(command)
	if dir := strings.TrimSpace(cwd); dir != "" {
		state += "\ncwd: " + dir
	}
	return state
}

// FetchNeuralDeepDecision asks the decisions endpoint whether the command is
// safe to run unattended. The HTTP read is bounded by the request timeout on
// top of ctx. The error is always a *NeuralDeepDecisionError, with the key
// redacted from any upstream text.
func FetchNeuralDeepDecision(ctx context.Context, apiBase, key, model, command, cwd string, proxyOrClient any) (*NeuralDeepDecision, error) {
	hc, err := providerHTTPClientArg(proxyOrClient)
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	payload, err := json.Marshal(neuralDeepDecisionRequest{
		Model:     strings.TrimSpace(model),
		State:     neuralDeepDecisionState(command, cwd),
		Questions: map[string]neuralDeepDecisionQuestionSpec{neuralDeepDecisionQuestionID: neuralDeepDecisionSafetyQuestion()},
	})
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	ctx, cancel := context.WithTimeout(ctx, neuralDeepDecisionRequestTimeout)
	defer cancel()
	url := strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/decisions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if readErr != nil {
		return nil, &NeuralDeepDecisionError{Status: resp.StatusCode, Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(readErr.Error())}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, &NeuralDeepDecisionError{Status: resp.StatusCode, Kind: NeuralDeepDecisionUnauthorized, Detail: neuralDeepDecisionDetail(body)}
	case resp.StatusCode == http.StatusForbidden:
		return nil, &NeuralDeepDecisionError{Status: resp.StatusCode, Kind: NeuralDeepDecisionForbidden, Detail: neuralDeepDecisionDetail(body)}
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &NeuralDeepDecisionError{
			Status:     resp.StatusCode,
			Kind:       NeuralDeepDecisionRateLimited,
			RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")),
			Detail:     neuralDeepDecisionDetail(body),
		}
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout:
		return nil, &NeuralDeepDecisionError{Status: resp.StatusCode, Kind: NeuralDeepDecisionRefused, Detail: neuralDeepDecisionDetail(body)}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &NeuralDeepDecisionError{
			Status:     resp.StatusCode,
			Kind:       NeuralDeepDecisionUnavailable,
			RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")),
			Detail:     neuralDeepDecisionDetail(body),
		}
	}
	decision, err := neuralDeepDecodeDecision(body)
	if err != nil {
		return nil, &NeuralDeepDecisionError{Status: resp.StatusCode, Kind: NeuralDeepDecisionInvalid, Detail: err.Error()}
	}
	return decision, nil
}

// neuralDeepDecodeDecision reads the safety answer out of a decisions
// response. The published examples render as images, so the decoder accepts
// the shapes the hub's own libraries produce: an answers map keyed by
// question id whose value is either a plain option name or an object with
// the chosen option under choice/decision/label and probabilities beside it.
// When no option is named, the most probable one stands in for it, and a
// named option the probabilities leave out counts as certain. The usage
// block's state_truncated flag (or the same flag at the top level) says the
// model read only the head of the state.
func neuralDeepDecodeDecision(body []byte) (*NeuralDeepDecision, error) {
	var top struct {
		Answers        map[string]json.RawMessage `json:"answers"`
		StateTruncated bool                       `json:"state_truncated"`
		Usage          struct {
			StateTruncated bool `json:"state_truncated"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, errors.New("undecodable payload")
	}
	raw, ok := top.Answers[neuralDeepDecisionQuestionID]
	if !ok {
		return nil, fmt.Errorf("payload without the %q answer", neuralDeepDecisionQuestionID)
	}
	var answer struct {
		Choice        string             `json:"choice"`
		Decision      string             `json:"decision"`
		Label         string             `json:"label"`
		Probabilities map[string]float64 `json:"probabilities"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		// A bare option name is a valid answer shape of its own.
		var named string
		if json.Unmarshal(raw, &named) == nil && named != "" {
			answer.Choice = named
		} else {
			return nil, errors.New("undecodable answer")
		}
	}
	choice := strings.TrimSpace(answer.Choice)
	if choice == "" {
		choice = strings.TrimSpace(answer.Decision)
	}
	if choice == "" {
		choice = strings.TrimSpace(answer.Label)
	}
	if choice == "" && len(answer.Probabilities) > 0 {
		best, at := "", 0.0
		for option, p := range answer.Probabilities {
			if p > at {
				best, at = option, p
			}
		}
		choice = best
	}
	if choice == "" {
		return nil, errors.New("answer without a chosen option")
	}
	probabilities := make(map[string]float64, len(answer.Probabilities)+1)
	for option, p := range answer.Probabilities {
		probabilities[option] = p
	}
	if _, ok := probabilities[choice]; !ok {
		probabilities[choice] = 1
	}
	return &NeuralDeepDecision{
		Choice:         choice,
		Probabilities:  probabilities,
		StateTruncated: top.StateTruncated || top.Usage.StateTruncated,
	}, nil
}

// neuralDeepDecisionDetail extracts a short, redacted description from an
// error body (the hub answers {"detail": "..."}).
func neuralDeepDecisionDetail(body []byte) string {
	detail := ""
	var env struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &env) == nil {
		detail = env.Detail
	}
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	if runes := []rune(detail); len(runes) > 160 {
		detail = string(runes[:160]) + "…"
	}
	return redactNeuralDeepSecrets(detail)
}

// NeuralDeepDecisionForProvider asks the decisions endpoint behind a
// neuraldeep provider row whether the command is safe, with the same
// credential and endpoint rules requests use: an explicit api_key (or
// command, or env) wins, the stored hub login fills in, the api_base selects
// the deployment, providers[].proxy applies. Without any credential it
// returns an unauthorized error without a request.
func NeuralDeepDecisionForProvider(ctx context.Context, provider config.ProviderConfig, authPath, model, command, cwd string) (*NeuralDeepDecision, error) {
	explicit, helperErr := provider.EffectiveAPIKeyContextErr(ctx)
	key := neuralDeepEffectiveKey(explicit, authPath)
	if strings.TrimSpace(key) == "" {
		if helperErr != nil {
			return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: "credential helper cut short: " + helperErr.Error()}
		}
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnauthorized, Detail: "no credential: sign in with coddy providers login " + provider.Name}
	}
	hc, err := HTTPClientForProviderProxy(provider.Proxy)
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	return FetchNeuralDeepDecision(ctx, neuralDeepAPIBase(provider.APIBase), key, model, command, cwd, hc)
}

// IsNeuralDeepDecisionError reports the failure kind of err when it is a
// decision fetch error.
func IsNeuralDeepDecisionError(err error) (*NeuralDeepDecisionError, bool) {
	var de *NeuralDeepDecisionError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}
