package llm

import (
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestDevinUsageProbe is a manual, opt-in probe: it runs only when
// CODDY_DEVIN_PROBE=1 is set and talks to the real Devin seat-management
// endpoint through the production DevinUsageForProvider path. It exists so a
// developer can check, against the live API, what GetUserStatus actually
// returns for their account (which fields carry the quota). It never runs in
// CI.
//
//	CODDY_DEVIN_PROBE=1 go test ./internal/llm -run TestDevinUsageProbe -v
//
// CODDY_DEVIN_PROBE_PROXY routes the probe through a proxy (for example
// socks5h://host:port) on networks that cannot reach the API directly.
// CODDY_DEVIN_PROBE_AUTH points at a devin-auth.json for a Coddy-managed
// login; without it the probe resolves CLI, env or explicit credentials only.
func TestDevinUsageProbe(t *testing.T) {
	if os.Getenv("CODDY_DEVIN_PROBE") != "1" {
		t.Skip("set CODDY_DEVIN_PROBE=1 to run the live Devin usage probe")
	}
	ctx := context.Background()
	authPath := os.Getenv("CODDY_DEVIN_PROBE_AUTH")
	cred, err := resolveDevinCredential("", authPath, true)
	if err != nil {
		t.Skipf("no devin credential on this machine: %v", err)
	}
	apiServer := devinAPIServer(cred.apiServer)
	t.Logf("credential source=%s apiServer=%q", cred.source, apiServer)

	hc, err := HTTPClientForOptionalProxy(os.Getenv("CODDY_DEVIN_PROBE_PROXY"))
	if err != nil {
		t.Fatalf("CODDY_DEVIN_PROBE_PROXY: %v", err)
	}
	if hc == nil {
		hc = http.DefaultClient
	}

	// The production path, through the provider's proxy client so the probe
	// exercises the same wiring a configured row would.
	provider := config.ProviderConfig{Name: "devin-probe", Proxy: os.Getenv("CODDY_DEVIN_PROBE_PROXY")}
	u, err := DevinUsageForProvider(ctx, provider, authPath, true)
	if err != nil {
		t.Fatalf("DevinUsageForProvider: %v", err)
	}
	t.Logf("plan=%q strategy=%d hasPlanStatus=%v daily%%=%d weekly%%=%d dailyReset=%d weeklyReset=%d acu=%v/%v",
		u.PlanName, u.BillingStrategy, u.HasPlanStatus,
		u.DailyRemainingPercent, u.WeeklyRemainingPercent, u.DailyResetAt, u.WeeklyResetAt,
		derefF(u.ACUConsumed), derefF(u.ACULimit))

	// A raw re-request for a full structural dump (diagnostics only).
	meta := devinMetadata{
		ide:       devinChatIDE,
		apiKey:    cred.token,
		sessionID: newCodexSessionID(),
		requestID: 1,
		triggerID: newCodexSessionID(),
	}
	var body pbWriter
	body.msg(1, meta.encode())
	raw, err := devinUnary(ctx, hc, apiServer+devinUsageGetUserStatus, body.buf)
	if err != nil {
		t.Fatalf("raw GetUserStatus: %v", err)
	}
	t.Logf("raw response %d bytes; top-level dump:", len(raw))
	dumpPB(t, raw, 0)
}

func derefF(f *float64) float64 {
	if f == nil {
		return -1
	}
	return *f
}

// dumpPB prints the field structure of a protobuf message for the probe log.
func dumpPB(t *testing.T, buf []byte, depth int) {
	t.Helper()
	indent := strings.Repeat("  ", depth)
	for len(buf) > 0 {
		key, n := binary.Uvarint(buf)
		if n <= 0 {
			t.Logf("%s<truncated key>", indent)
			return
		}
		buf = buf[n:]
		num, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, m := binary.Uvarint(buf)
			if m <= 0 {
				t.Logf("%sf%d: truncated varint", indent, num)
				return
			}
			t.Logf("%sf%d varint = %d", indent, num, v)
			buf = buf[m:]
		case 2:
			l, m := binary.Uvarint(buf)
			if m <= 0 || l > uint64(len(buf)-m) {
				t.Logf("%sf%d: truncated bytes", indent, num)
				return
			}
			raw := buf[m : m+int(l)]
			buf = buf[m+int(l):]
			if isPrintable(raw) {
				t.Logf("%sf%d bytes = %q", indent, num, string(raw))
			} else {
				t.Logf("%sf%d msg (%d bytes):", indent, num, l)
				dumpPB(t, raw, depth+1)
			}
		case 1:
			if len(buf) < 8 {
				return
			}
			t.Logf("%sf%d fixed64 = %v", indent, num, math.Float64frombits(binary.LittleEndian.Uint64(buf)))
			buf = buf[8:]
		case 5:
			if len(buf) < 4 {
				return
			}
			t.Logf("%sf%d fixed32", indent, num)
			buf = buf[4:]
		default:
			t.Logf("%sf%d: unknown wire %d", indent, num, wire)
			return
		}
	}
}

func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
