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

// NeuralDeepDecisionSubject is what the safety question is asked about: a
// shell command and where it would run - the local working directory of a
// run_command, or the remote host of an ssh_run_command.
type NeuralDeepDecisionSubject struct {
	Command string
	CWD     string
	Host    string
}

// neuralDeepDecisionState frames the command the endpoint judges: the command
// itself plus where it would run. Nothing here bounds it: the hub cuts a
// state longer than the model reads and reports the cut, which the decision
// carries as StateTruncated.
func neuralDeepDecisionState(subject NeuralDeepDecisionSubject) string {
	state := "command: " + strings.TrimSpace(subject.Command)
	if host := strings.TrimSpace(subject.Host); host != "" {
		state += "\nruns on the remote host (over SSH): " + host
	}
	if dir := strings.TrimSpace(subject.CWD); dir != "" {
		state += "\ncwd: " + dir
	}
	return state
}

// FetchNeuralDeepDecision asks the decisions endpoint whether the command is
// safe to run unattended. The HTTP read is bounded by the request timeout on
// top of ctx. The error is always a *NeuralDeepDecisionError, with the key
// redacted from any upstream text.
func FetchNeuralDeepDecision(ctx context.Context, apiBase, key, model string, subject NeuralDeepDecisionSubject, proxyOrClient any) (*NeuralDeepDecision, error) {
	hc, err := providerHTTPClientArg(proxyOrClient)
	if err != nil {
		return nil, &NeuralDeepDecisionError{Kind: NeuralDeepDecisionUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	payload, err := json.Marshal(neuralDeepDecisionRequest{
		Model:     strings.TrimSpace(model),
		State:     neuralDeepDecisionState(subject),
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
// response. The decoder accepts the shapes the hub's own libraries produce:
// an answers map keyed by question id whose value is either a plain option
// name or an object with the chosen option under choice/decision/label and
// probabilities beside it. Option names are compared trimmed and in lower
// case. The unsafe probability is the larger of the answer's own and the
// complement of the safe one, so mass the answer leaves to neither option
// counts against the command; with one of the two missing the other's
// complement stands in, and a bare answer is certain of the option it names.
// When no option is named, the more probable one stands in for it. An answer
// the question cannot have produced - an option it does not offer, one option
// spelled twice, a probability outside 0..1, probabilities of neither option -
// is an error, never a verdict, so a missing number is not read as "safe". The usage block's state_truncated
// flag (or the same flag at the top level) says the model read only the head
// of the state.
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
	given := make(map[string]float64, len(answer.Probabilities))
	for option, p := range answer.Probabilities {
		if p < 0 || p > 1 {
			return nil, fmt.Errorf("probability %v of option %q is outside 0..1", p, option)
		}
		name := neuralDeepDecisionOption(option)
		if _, dup := given[name]; dup {
			// Which spelling wins would depend on map order.
			return nil, fmt.Errorf("answer gives the option %q twice", name)
		}
		given[name] = p
	}
	choice := neuralDeepDecisionOption(answer.Choice)
	if choice == "" {
		choice = neuralDeepDecisionOption(answer.Decision)
	}
	if choice == "" {
		choice = neuralDeepDecisionOption(answer.Label)
	}
	if choice != "" && choice != NeuralDeepDecisionSafe && choice != NeuralDeepDecisionUnsafe {
		return nil, fmt.Errorf("answer names the option %q the safety question does not offer", choice)
	}
	pSafe, hasSafe := given[NeuralDeepDecisionSafe]
	pUnsafe, hasUnsafe := given[NeuralDeepDecisionUnsafe]
	switch {
	case hasUnsafe && hasSafe:
		pUnsafe = max(pUnsafe, 1-pSafe)
	case hasUnsafe && !hasSafe:
		pSafe = 1 - pUnsafe
	case hasSafe && !hasUnsafe:
		pUnsafe = 1 - pSafe
	case !hasSafe && !hasUnsafe:
		if len(given) > 0 {
			return nil, errors.New("answer without a probability of either option")
		}
		if choice == "" {
			return nil, errors.New("answer without a chosen option")
		}
		// A bare answer is certain of the option it names.
		pUnsafe = 0
		if choice == NeuralDeepDecisionUnsafe {
			pUnsafe = 1
		}
		pSafe = 1 - pUnsafe
	}
	if choice == "" {
		choice = NeuralDeepDecisionSafe
		if pUnsafe >= pSafe {
			choice = NeuralDeepDecisionUnsafe
		}
	}
	return &NeuralDeepDecision{
		Choice:         choice,
		Probabilities:  map[string]float64{NeuralDeepDecisionSafe: pSafe, NeuralDeepDecisionUnsafe: pUnsafe},
		StateTruncated: top.StateTruncated || top.Usage.StateTruncated,
	}, nil
}

// neuralDeepDecisionOption spells an option name the way the safety question
// does.
func neuralDeepDecisionOption(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
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
func NeuralDeepDecisionForProvider(ctx context.Context, provider config.ProviderConfig, authPath, model string, subject NeuralDeepDecisionSubject) (*NeuralDeepDecision, error) {
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
	return FetchNeuralDeepDecision(ctx, neuralDeepAPIBase(provider.APIBase), key, model, subject, hc)
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
