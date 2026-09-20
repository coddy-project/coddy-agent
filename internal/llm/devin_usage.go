package llm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Devin subscription usage: the account quota behind a devin provider, read
// from the seat-management RPC the editor itself calls. The wire shape was
// verified against the official Devin editor 3.10.31 descriptors; see
// docs/plans/neuraldeep-usage.md for the shared design.
//
// GetUserStatusResponse carries a UserStatus (field 1) and a PlanInfo
// (field 2). The quota numbers live on UserStatus.plan_status (field 13):
// its plan_info (field 1) names the plan and billing strategy, and the
// remaining-percent / reset fields sit next to it. The top-level PlanInfo is
// the fallback a plan-only response uses.

const (
	// devinUsageGetUserStatus is the seat-management RPC the editor polls
	// for the signed-in account's plan and quota state.
	devinUsageGetUserStatus = "/exa.seat_management_pb.SeatManagementService/GetUserStatus"

	// devinUsageBodyLimit bounds the status answer; a real payload is a few
	// hundred bytes, so anything past this is corrupt or hostile.
	devinUsageBodyLimit = 256 << 10
)

// DevinUsageQuotaBillingStrategy is the daily/weekly quota plan the
// remaining-percent fields describe (PlanInfo.billing_strategy, field 35;
// credits = 1, ACU = 3).
const DevinUsageQuotaBillingStrategy = 2

// DevinUsage is the decoded GetUserStatus answer. The percent fields are the
// server's own "remaining" numbers; the session layer turns them into used
// percents and decides which windows the strategy entitles to a meter.
type DevinUsage struct {
	PlanName               string
	BillingStrategy        int
	HasPlanStatus          bool
	HideDailyQuota         bool
	HideWeeklyQuota        bool
	DailyRemainingPercent  uint64
	WeeklyRemainingPercent uint64
	DailyResetAt           int64
	WeeklyResetAt          int64
	ACUConsumed            *float64
	ACULimit               *float64
}

// DevinUsageForProvider reads the account status of a devin provider. The
// credential resolves exactly like a chat call (api_key, helper command,
// environment, managed login, Devin CLI) and the user JWT minted for chat is
// reused through devinJWTFor.
func DevinUsageForProvider(ctx context.Context, provider config.ProviderConfig, authPath string) (*DevinUsage, error) {
	key, keyErr := provider.EffectiveAPIKeyContextErr(ctx)
	cred, err := resolveDevinCredential(key, authPath)
	if err != nil {
		kind := ProviderUsageUnauthorized
		// A configured credential helper that produced nothing is a broken
		// setup, not a missing login.
		if keyErr != nil || strings.TrimSpace(provider.APIKeyCommand) != "" {
			kind = ProviderUsageUnavailable
		}
		return nil, &ProviderUsageError{Kind: kind, Detail: "credential unavailable"}
	}
	hc, err := HTTPClientForProviderProxy(provider.Proxy)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "invalid proxy configuration"}
	}
	// A redirect would forward the session token to another host.
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	jwt, err := devinJWTFor(ctx, &client, cred)
	if err != nil {
		return nil, devinUsageCallError(err)
	}
	meta := devinMetadata{
		ide:       devinChatIDE,
		apiKey:    cred.token,
		userJWT:   jwt.jwt,
		sessionID: newCodexSessionID(),
		requestID: uint64(time.Now().UnixMilli()),
		triggerID: newCodexSessionID(),
	}
	var w pbWriter
	w.msg(1, meta.encode())
	raw, err := devinUnary(ctx, &client, devinAPIServer(cred.apiServer)+devinUsageGetUserStatus, w.buf)
	if err != nil {
		return nil, devinUsageCallError(err)
	}
	if len(raw) > devinUsageBodyLimit {
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "payload too large"}
	}
	usage, err := decodeDevinUserStatus(raw)
	if err != nil {
		// Some deployments answer a Connect error envelope with a 200.
		if env := devinErrorFromBody(http.StatusOK, raw); env.code == "unauthenticated" {
			return nil, &ProviderUsageError{Kind: ProviderUsageUnauthorized, Detail: "upstream error"}
		}
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "invalid payload"}
	}
	return usage, nil
}

// devinUsageCallError maps a failed RPC to a provider-neutral usage error.
// The upstream message is never echoed: it can carry anything.
func devinUsageCallError(err error) error {
	var apiErr *devinAPIError
	if errors.As(err, &apiErr) {
		kind := ProviderUsageUnavailable
		if apiErr.status == http.StatusUnauthorized || apiErr.code == "unauthenticated" {
			kind = ProviderUsageUnauthorized
		}
		return &ProviderUsageError{Status: apiErr.status, Kind: kind, RetryAfter: apiErr.retryAfter, Detail: "upstream error"}
	}
	var bad *devinInvalidResponseError
	if errors.As(err, &bad) {
		return &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "invalid payload"}
	}
	return &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "request failed"}
}

// decodeDevinUserStatus parses a GetUserStatusResponse. Fields it does not
// know are skipped; fields it knows are checked against their wire type so a
// shifted schema fails loudly instead of silently misreading the account.
func decodeDevinUserStatus(raw []byte) (*DevinUsage, error) {
	fields, err := pbFields(raw)
	if err != nil {
		return nil, fmt.Errorf("devin usage: undecodable response")
	}
	u := &DevinUsage{}
	var statusFields []pbField
	for _, f := range fields {
		switch f.field {
		case 0:
			return nil, fmt.Errorf("devin usage: field 0 in response")
		case 1: // UserStatus
			if f.wire != pbBytes {
				return nil, fmt.Errorf("devin usage: user_status wire type %d", f.wire)
			}
			inner, err := pbFields(f.raw)
			if err != nil {
				return nil, fmt.Errorf("devin usage: undecodable user_status")
			}
			for _, uf := range inner {
				if uf.field == 0 {
					return nil, fmt.Errorf("devin usage: field 0 in user_status")
				}
				if uf.field == 13 { // plan_status
					if uf.wire != pbBytes {
						return nil, fmt.Errorf("devin usage: plan_status wire type %d", uf.wire)
					}
					statusFields, err = pbFields(uf.raw)
					if err != nil {
						return nil, fmt.Errorf("devin usage: undecodable plan_status")
					}
					u.HasPlanStatus = true
				}
				// Field 3 is the account display name and field 12 the
				// retired plan_info: neither names the plan.
			}
		case 2: // PlanInfo fallback
			if f.wire != pbBytes {
				return nil, fmt.Errorf("devin usage: plan_info wire type %d", f.wire)
			}
			if err := decodeDevinPlanInfo(f.raw, u); err != nil {
				return nil, err
			}
		}
	}
	for _, f := range statusFields {
		switch f.field {
		case 0:
			return nil, fmt.Errorf("devin usage: field 0 in plan_status")
		case 1: // plan_info wins over the top-level PlanInfo
			if f.wire != pbBytes {
				return nil, fmt.Errorf("devin usage: plan_status.plan_info wire type %d", f.wire)
			}
			if err := decodeDevinPlanInfo(f.raw, u); err != nil {
				return nil, err
			}
		case 14:
			v, err := devinUsagePercentField(f)
			if err != nil {
				return nil, err
			}
			u.DailyRemainingPercent = v
		case 15:
			v, err := devinUsagePercentField(f)
			if err != nil {
				return nil, err
			}
			u.WeeklyRemainingPercent = v
		case 17:
			v, err := devinUsageResetField(f)
			if err != nil {
				return nil, err
			}
			u.DailyResetAt = v
		case 18:
			v, err := devinUsageResetField(f)
			if err != nil {
				return nil, err
			}
			u.WeeklyResetAt = v
		case 19:
			v, err := devinUsageACUField(f)
			if err != nil {
				return nil, err
			}
			u.ACUConsumed = &v
		case 20:
			v, err := devinUsageACUField(f)
			if err != nil {
				return nil, err
			}
			u.ACULimit = &v
		}
		// Field 16 (overage balance micros) is money, not a request count.
	}
	if strings.TrimSpace(u.PlanName) == "" {
		return nil, fmt.Errorf("devin usage: response without a plan name")
	}
	return u, nil
}

// decodeDevinPlanInfo reads a PlanInfo (name, billing strategy, quota
// visibility) into u.
func decodeDevinPlanInfo(raw []byte, u *DevinUsage) error {
	fields, err := pbFields(raw)
	if err != nil {
		return fmt.Errorf("devin usage: undecodable plan_info")
	}
	for _, f := range fields {
		switch f.field {
		case 0:
			return fmt.Errorf("devin usage: field 0 in plan_info")
		case 2:
			if f.wire != pbBytes {
				return fmt.Errorf("devin usage: plan_name wire type %d", f.wire)
			}
			u.PlanName = f.text()
		case 35:
			if f.wire != pbVarint {
				return fmt.Errorf("devin usage: billing_strategy wire type %d", f.wire)
			}
			if f.num > math.MaxInt64 {
				return fmt.Errorf("devin usage: billing_strategy out of range")
			}
			u.BillingStrategy = int(f.num)
		case 36:
			if f.wire != pbVarint {
				return fmt.Errorf("devin usage: hide_daily_quota wire type %d", f.wire)
			}
			u.HideDailyQuota = f.num != 0
		case 37:
			if f.wire != pbVarint {
				return fmt.Errorf("devin usage: hide_weekly_quota wire type %d", f.wire)
			}
			u.HideWeeklyQuota = f.num != 0
		}
	}
	return nil
}

// devinUsagePercentField reads a remaining-percent varint (0-100).
func devinUsagePercentField(f pbField) (uint64, error) {
	if f.wire != pbVarint {
		return 0, fmt.Errorf("devin usage: remaining percent wire type %d", f.wire)
	}
	if f.num > 100 {
		return 0, fmt.Errorf("devin usage: remaining percent %d out of range", f.num)
	}
	return f.num, nil
}

// devinUsageResetField reads a unix reset timestamp varint.
func devinUsageResetField(f pbField) (int64, error) {
	if f.wire != pbVarint {
		return 0, fmt.Errorf("devin usage: reset wire type %d", f.wire)
	}
	if f.num > math.MaxInt64 {
		return 0, fmt.Errorf("devin usage: negative reset timestamp")
	}
	return int64(f.num), nil
}

// devinUsageACUField reads an optional ACU double (consumed or limit).
func devinUsageACUField(f pbField) (float64, error) {
	if f.wire != pbFixed64 {
		return 0, fmt.Errorf("devin usage: acu wire type %d", f.wire)
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(f.raw))
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, fmt.Errorf("devin usage: acu value out of range")
	}
	return v, nil
}

// DevinUsageFingerprint identifies the credential behind a devin provider for
// the usage cache. It resolves the same source order a fetch does but never
// executes a credential helper: a configured api_key_command is fingerprinted
// by its text, not run.
func DevinUsageFingerprint(provider config.ProviderConfig, authPath string) string {
	material, apiServer := devinUsageCredentialIdentity(provider, authPath)
	if material == "" {
		return ""
	}
	h := sha256.New()
	writeField := func(s string) {
		_, _ = io.WriteString(h, s)
		_, _ = h.Write([]byte{0})
	}
	writeField("type:devin")
	writeField("endpoint:" + devinAPIServer(apiServer))
	writeField("proxy:" + strings.TrimSpace(provider.Proxy))
	writeField("credential:" + material)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// devinUsageCredentialIdentity picks the credential material a fetch would
// use, in resolveDevinCredential's order, without running helper commands.
// The string it returns is hashed by the caller, never printed.
func devinUsageCredentialIdentity(provider config.ProviderConfig, authPath string) (material, apiServer string) {
	if key := strings.TrimSpace(provider.APIKey); key != "" {
		return "api-key:" + normalizeDevinToken(key), ""
	}
	if cmd := strings.TrimSpace(provider.APIKeyCommand); cmd != "" {
		return "api-key-command:" + cmd, ""
	}
	if env := strings.TrimSpace(os.Getenv(config.ProviderAPIKeyEnvVarName(provider.Name))); env != "" {
		return "env:" + normalizeDevinToken(env), ""
	}
	if f, err := loadDevinAuth(authPath); err == nil && f != nil && strings.TrimSpace(f.SessionToken) != "" {
		return "managed:" + normalizeDevinToken(f.SessionToken), f.APIServerURL
	}
	cli, path, err := loadDevinCLICredentials()
	if err == nil && cli != nil && strings.TrimSpace(cli.APIKey) != "" {
		return "cli:" + path + ":" + normalizeDevinToken(cli.APIKey), cli.APIServerURL
	}
	return "", ""
}
