package pki

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

func want() Want {
	return Want{Host: "box", Names: []string{"box", "localhost", "127.0.0.1", "::1", "relay.example"}}
}

func ensureOK(t *testing.T, dir string, w Want) Result {
	t.Helper()
	res, err := Ensure(dir, w, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return res
}

func actions(steps []Step) []Action {
	var out []Action
	for _, s := range steps {
		out = append(out, s.Action)
	}
	return out
}

// valid checks the invariants the planner promises about a directory it has made right: the plan is empty, every certificate has its
// key, the leaves chain to the CA and carry the right usage, and the bundle holds the CA.
func valid(t *testing.T, dir string, w Want) State {
	t.Helper()
	st := Load(dir)
	if steps := Plan(st, w, time.Now()); len(steps) != 0 {
		t.Fatalf("the plan is not empty: %+v", steps)
	}
	if st.CA == nil || !st.CAKeyOK || st.Server == nil || !st.Server.KeyMatches || st.Client == nil || !st.Client.KeyMatches {
		t.Fatalf("the set is incomplete: %+v", st)
	}
	pool := x509.NewCertPool()
	pool.AddCert(st.CA)
	if _, err := st.Server.Cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSName: "box"}); err != nil {
		t.Fatalf("the server certificate does not verify for its host name: %v", err)
	}
	if _, err := st.Client.Cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("the client certificate does not verify: %v", err)
	}
	if !strings.Contains(string(st.Bundle), string(CertPEM(st.CA))) {
		t.Fatal("the bundle does not hold the CA")
	}
	return st
}

func TestPlanOfAnEmptyStateMakesEverything(t *testing.T) {
	got := actions(Plan(State{}, want(), time.Now()))
	if !reflect.DeepEqual(got, []Action{CreateCA, IssueServer, IssueClient, WriteBundle}) {
		t.Fatalf("plan = %v", got)
	}
}

func TestEnsureMakesAWorkingSetAndASecondRunDoesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	res := ensureOK(t, dir, want())
	if !res.NewCA || len(res.Steps) != 4 {
		t.Fatalf("first run: %+v", res)
	}
	st := valid(t, dir, want())
	before := map[string][]byte{}
	for _, n := range []string{CAFile, CAKeyFile, ServerCertFile, ServerKeyFile, ClientCertFile, ClientKeyFile, BundleFile} {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		before[n] = raw
	}
	if res := ensureOK(t, dir, want()); res.Changed() {
		t.Fatalf("a second run did %+v: ensure must be idempotent", res.Steps)
	}
	for n, raw := range before {
		now, _ := os.ReadFile(filepath.Join(dir, n))
		if string(now) != string(raw) {
			t.Errorf("%s changed on a run that had nothing to do", n)
		}
	}
	if st.CA.MaxPathLen != 0 || !st.CA.MaxPathLenZero || !st.CA.IsCA {
		t.Errorf("the CA must sign leaves and nothing below them: %+v", st.CA)
	}
	if runtime.GOOS != "windows" {
		for n, mode := range map[string]os.FileMode{CAKeyFile: 0o600, ServerKeyFile: 0o600, ClientKeyFile: 0o600, CAFile: 0o644, ServerCertFile: 0o644} {
			if info, _ := os.Stat(filepath.Join(dir, n)); info == nil || info.Mode().Perm() != mode {
				t.Errorf("%s mode = %v, want %v", n, info.Mode().Perm(), mode)
			}
		}
		if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
			t.Errorf("the directory mode = %v, want 0700", info.Mode().Perm())
		}
	}
}

// Each row breaks one thing in a good set and says what the plan must do about it, and only that.
func TestPlanRepairsOnlyWhatIsWrong(t *testing.T) {
	cases := []struct {
		name    string
		damage  func(t *testing.T, dir string)
		w       func(Want) Want
		now     func(time.Time) time.Time
		want    []Action
		sameCA  bool
		keepSrv bool
	}{
		{name: "nothing wrong", damage: func(*testing.T, string) {}, want: nil, sameCA: true, keepSrv: true},
		{name: "the server key is gone", damage: func(t *testing.T, d string) { remove(t, d, ServerKeyFile) }, want: []Action{IssueServer}, sameCA: true},
		{name: "the server certificate is gone", damage: func(t *testing.T, d string) { remove(t, d, ServerCertFile) }, want: []Action{IssueServer}, sameCA: true},
		{name: "the client certificate is garbage", damage: func(t *testing.T, d string) { write(t, d, ClientCertFile, "not pem") }, want: []Action{IssueClient}, sameCA: true, keepSrv: true},
		{name: "a name is wanted that the server certificate lacks", damage: func(*testing.T, string) {}, w: func(w Want) Want { w.Names = append(w.Names, "new.example"); return w },
			want: []Action{IssueServer}, sameCA: true},
		{name: "a name is no longer wanted: not a reason", damage: func(*testing.T, string) {}, w: func(w Want) Want { w.Names = []string{"box"}; return w },
			want: nil, sameCA: true, keepSrv: true},
		{name: "the leaves are within the renewal window", damage: func(*testing.T, string) {}, now: func(n time.Time) time.Time { return n.Add(DefaultLeafValidity - 10*24*time.Hour) },
			want: []Action{IssueServer, IssueClient}, sameCA: true},
		{name: "the leaves have expired, the CA has not", damage: func(*testing.T, string) {}, now: func(n time.Time) time.Time { return n.Add(DefaultLeafValidity + time.Hour) },
			want: []Action{IssueServer, IssueClient}, sameCA: true},
		{name: "the CA key is gone", damage: func(t *testing.T, d string) { remove(t, d, CAKeyFile) }, want: []Action{CreateCA, IssueServer, IssueClient, WriteBundle}},
		{name: "the CA has expired", damage: func(*testing.T, string) {}, now: func(n time.Time) time.Time { return n.Add(DefaultCAValidity + time.Hour) },
			want: []Action{RenewCA, IssueServer, IssueClient, WriteBundle}},
		{name: "forced", damage: func(*testing.T, string) {}, w: func(w Want) Want { w.ForceCA = true; return w }, want: []Action{RenewCA, IssueServer, IssueClient, WriteBundle}},
		{name: "renewal asked for", damage: func(*testing.T, string) {}, w: func(w Want) Want { w.RenewLeaves = true; return w }, want: []Action{IssueServer, IssueClient}, sameCA: true},
		{name: "the bundle is stale", damage: func(t *testing.T, d string) { write(t, d, BundleFile, "stale") }, want: []Action{WriteBundle}, sameCA: true, keepSrv: true},
		{name: "the bundle is gone", damage: func(t *testing.T, d string) { remove(t, d, BundleFile) }, want: []Action{WriteBundle}, sameCA: true, keepSrv: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			ensureOK(t, dir, want())
			caBefore, _ := os.ReadFile(filepath.Join(dir, CAFile))
			srvBefore, _ := os.ReadFile(filepath.Join(dir, ServerCertFile))
			c.damage(t, dir)
			w := want()
			if c.w != nil {
				w = c.w(w)
			}
			now := time.Now()
			if c.now != nil {
				now = c.now(now)
			}
			got := actions(Plan(Load(dir), w, now))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("plan = %v, want %v", got, c.want)
			}
			if len(got) == 0 {
				return
			}
			if _, err := Ensure(dir, w, func() time.Time { return now }); err != nil {
				t.Fatal(err)
			}
			if c.now == nil {
				w.ForceCA, w.RenewLeaves = false, false
				valid(t, dir, w)
			}
			caAfter, _ := os.ReadFile(filepath.Join(dir, CAFile))
			if c.sameCA && string(caAfter) != string(caBefore) {
				t.Error("a valid CA was replaced")
			}
			if !c.sameCA && string(caAfter) == string(caBefore) {
				t.Error("the CA should have been replaced")
			}
			srvAfter, _ := os.ReadFile(filepath.Join(dir, ServerCertFile))
			if c.keepSrv && string(srvAfter) != string(srvBefore) {
				t.Error("a server certificate that was fine was issued again")
			}
		})
	}
}

func remove(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A certificate that another CA signed is not this CA's: the CA stays, the leaf is issued again.
func TestALeafSignedByAnotherCAIsIssuedAgain(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	ensureOK(t, a, want())
	ensureOK(t, b, want())
	for _, n := range []string{ServerCertFile, ServerKeyFile} {
		raw, _ := os.ReadFile(filepath.Join(b, n))
		write(t, a, n, string(raw))
	}
	caBefore, _ := os.ReadFile(filepath.Join(a, CAFile))
	res := ensureOK(t, a, want())
	if !reflect.DeepEqual(actions(res.Steps), []Action{IssueServer}) {
		t.Fatalf("steps = %v", actions(res.Steps))
	}
	if caAfter, _ := os.ReadFile(filepath.Join(a, CAFile)); string(caAfter) != string(caBefore) {
		t.Fatal("the CA was replaced")
	}
	valid(t, a, want())
}

func TestForceCAIssuesEverythingOnceAndTheNextRunDoesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	ensureOK(t, dir, want())
	old := Load(dir).CA
	w := want()
	w.ForceCA = true
	res := ensureOK(t, dir, w)
	if !res.NewCA {
		t.Fatalf("no new CA: %+v", res)
	}
	st := valid(t, dir, want())
	if Fingerprint(st.CA) == Fingerprint(old) {
		t.Fatal("the CA is the same after a forced renewal")
	}
	if res := ensureOK(t, dir, want()); res.Changed() {
		t.Fatalf("the run after a forced renewal did %+v", res.Steps)
	}
}

// failingFS fails the n-th write (from 1) and every one after it, like a disk that fills or a process that dies at that point.
type failingFS struct {
	osFS
	n, calls int
}

func (f *failingFS) writeFile(path string, data []byte, perm os.FileMode) error {
	f.calls++
	if f.n > 0 && f.calls >= f.n {
		return errors.New("injected failure")
	}
	return f.osFS.writeFile(path, data, perm)
}

// A crash at any write leaves a directory the next run repairs, and never a certificate without its key.
func TestACrashAtAnyWriteLeavesARepairableState(t *testing.T) {
	// Count the writes of a clean run first.
	counter := &failingFS{}
	if _, err := ensure(filepath.Join(t.TempDir(), "tls"), want(), nil, counter); err != nil {
		t.Fatal(err)
	}
	total := counter.calls
	if total < 7 {
		t.Fatalf("a clean run made %d writes, want at least 7", total)
	}
	for k := 1; k <= total; k++ {
		t.Run(fmt.Sprintf("write %d fails", k), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			if _, err := ensure(dir, want(), nil, &failingFS{n: k}); err == nil {
				t.Fatal("the injected failure was not reported")
			}
			// No certificate without its key, at any moment.
			for _, pair := range [][2]string{{CAFile, CAKeyFile}, {ServerCertFile, ServerKeyFile}, {ClientCertFile, ClientKeyFile}} {
				if exists(dir, pair[0]) && !exists(dir, pair[1]) {
					t.Errorf("%s exists without %s", pair[0], pair[1])
				}
			}
			// No temporary file is left behind.
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".") && e.Name() != lockName {
					t.Errorf("a temporary file is left: %s", e.Name())
				}
			}
			ensureOK(t, dir, want())
			valid(t, dir, want())
		})
	}
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// Several callers at once (the installer, an update, a service start, an operator) end with one CA and a valid set.
func TestConcurrentEnsureEndsWithOneCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Ensure(dir, want(), nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	valid(t, dir, want())
	if n := countCAs(t, dir); n != 1 {
		t.Fatalf("%d CAs in the bundle, want 1", n)
	}
}

func countCAs(t *testing.T, dir string) int {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(dir, BundleFile))
	n := 0
	for {
		var b *pem.Block
		b, raw = pem.Decode(raw)
		if b == nil {
			return n
		}
		n++
	}
}

// The files work as TLS material: a server with this machine's pair, a client with another machine's pair, and the CAs exchanged.
func TestTwoMachinesTrustEachOtherByExchangingCAs(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	wa := Want{Host: "box-a", Names: []string{"box-a", "localhost", "127.0.0.1"}}
	wb := Want{Host: "box-b", Names: []string{"box-b", "localhost", "127.0.0.1"}}
	ensureOK(t, a, wa)
	ensureOK(t, b, wb)

	serve := func(dir string) *httptest.Server {
		p := PathsIn(dir)
		cfg, err := netx.ClientCertTLS(p.Bundle, "test.client_ca_file")
		if err != nil {
			t.Fatal(err)
		}
		pair, err := tls.LoadX509KeyPair(p.ServerCert, p.ServerKey)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Certificates = []tls.Certificate{pair}
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
		ts.TLS = cfg
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return ts
	}
	get := func(url, dir, host string) error {
		p := PathsIn(dir)
		cfg, err := (netx.Options{CAFile: p.Bundle, CertFile: p.ClientCert, KeyFile: p.ClientKey}).TLSConfig(host)
		if err != nil {
			return err
		}
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
		resp, err := c.Get(url)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
	srv := serve(a)
	if err := get(srv.URL, b, "127.0.0.1"); err == nil {
		t.Fatal("machine b reached machine a before it trusted its CA")
	}
	// b trusts a's CA, so b verifies a's server certificate; but a still does not trust b's client certificate.
	caA, _ := ExportCA(a)
	if added, _, err := Trust(b, caA, time.Now()); err != nil || !added {
		t.Fatalf("trust a in b: %v %v", added, err)
	}
	if err := get(srv.URL, b, "127.0.0.1"); err == nil {
		t.Fatal("machine a admitted a client certificate of a CA it does not trust")
	}
	caB, _ := ExportCA(b)
	if added, _, err := Trust(a, caB, time.Now()); err != nil || !added {
		t.Fatalf("trust b in a: %v %v", added, err)
	}
	// a's listener reads its CA bundle at start: a running one needs a restart, so start it again.
	srv = serve(a)
	if err := get(srv.URL, b, "127.0.0.1"); err != nil {
		t.Fatalf("after the exchange of CAs: %v", err)
	}
	// Trusting the same CA twice adds nothing.
	if added, _, err := Trust(a, caB, time.Now()); err != nil || added {
		t.Fatalf("a second trust: added=%v err=%v", added, err)
	}
	if n := countCAs(t, a); n != 2 {
		t.Fatalf("%d CAs in a's bundle, want its own and b's", n)
	}
}

func TestTrustRefusesALeafAKeyAndAnExpiredCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	ensureOK(t, dir, want())
	st := Load(dir)
	if _, _, err := Trust(dir, CertPEM(st.Server.Cert), time.Now()); !errors.Is(err, ErrNotCA) {
		t.Errorf("a leaf: %v, want ErrNotCA", err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, CAKeyFile))
	if _, _, err := Trust(dir, key, time.Now()); err == nil || !strings.Contains(err.Error(), "private key") {
		t.Errorf("a private key: %v", err)
	}
	if _, _, err := Trust(dir, []byte("nothing"), time.Now()); err == nil {
		t.Error("no certificate at all was accepted")
	}
	other := filepath.Join(t.TempDir(), "o")
	ensureOK(t, other, want())
	ca, _ := ExportCA(other)
	if _, _, err := Trust(dir, ca, time.Now().Add(DefaultCAValidity+time.Hour)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired CA: %v", err)
	}
}

func TestIssueClientFilesIsSignedByTheCAAndKeepsItsKey(t *testing.T) {
	dir, out := filepath.Join(t.TempDir(), "tls"), filepath.Join(t.TempDir(), "out")
	ensureOK(t, dir, want())
	certPath, keyPath, caPath, err := IssueClientFiles(dir, "ci-runner", out, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, k := readCertAndKey(certPath, keyPath)
	ca := readCert(caPath)
	if c == nil || k == nil || ca == nil || !keyMatches(c, k) {
		t.Fatal("the files do not hold a certificate, its key and the CA")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("the client certificate does not verify against the exported CA: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(keyPath); info.Mode().Perm() != 0o600 {
			t.Errorf("the key mode = %v", info.Mode().Perm())
		}
	}
	for _, bad := range []string{"", "../escape", "a b", strings.Repeat("x", 64), ".hidden"} {
		if _, _, _, err := IssueClientFiles(dir, bad, out, time.Now(), 0); err == nil {
			t.Errorf("the name %q was accepted", bad)
		}
	}
	if _, _, _, err := IssueClientFiles(filepath.Join(t.TempDir(), "empty"), "x", out, time.Now(), 0); err == nil {
		t.Error("a certificate was issued with no CA")
	}
}

func TestDefaultNames(t *testing.T) {
	addrs := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}, // link-local: skipped
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("0.0.0.0"), Mask: net.CIDRMask(0, 32)}, // not a name
		}, nil
	}
	got := DefaultNames("Box", addrs, []string{"0.0.0.0", "https://Relay.Example:12346/x", "10.0.0.5:9000", "[::]:80", "", "plainhost"}, []string{"extra.example", " "})
	wantNames := []string{"127.0.0.1", "192.168.1.20", "10.0.0.5", "::1", "box", "extra.example", "localhost", "plainhost", "relay.example"}
	if !reflect.DeepEqual(NormalizeNames(got), NormalizeNames(wantNames)) {
		t.Fatalf("names = %v, want %v", got, wantNames)
	}
	for _, n := range got {
		if ip := net.ParseIP(n); ip != nil && ip.IsUnspecified() {
			t.Errorf("a wildcard address is a name: %s", n)
		}
	}
	// Many interfaces: the number of addresses is bounded.
	var many []net.Addr
	for i := 1; i <= 40; i++ {
		many = append(many, &net.IPNet{IP: net.ParseIP(fmt.Sprintf("10.1.0.%d", i)), Mask: net.CIDRMask(24, 32)})
	}
	count := 0
	for _, n := range DefaultNames("box", func() ([]net.Addr, error) { return many, nil }, nil, nil) {
		if strings.HasPrefix(n, "10.1.0.") {
			count++
		}
	}
	if count != maxInterfaceIPs {
		t.Errorf("%d interface addresses, want the bound %d", count, maxInterfaceIPs)
	}
}

func TestDescribeAndFindings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if f := Describe(dir, want(), time.Now()).Findings(0); len(f) != 1 || !strings.Contains(f[0], "coddy tls ensure") {
		t.Fatalf("an empty directory: %v", f)
	}
	ensureOK(t, dir, want())
	s := Describe(dir, want(), time.Now())
	if f := s.Findings(0); len(f) != 0 {
		t.Fatalf("a good set has findings: %v", f)
	}
	if !s.CA.Present || !s.CAKeyOK || !s.BundleOK || s.Server.DaysLeft < 360 || len(s.Pending) != 0 {
		t.Fatalf("status = %+v", s)
	}
	late := Describe(dir, want(), time.Now().Add(DefaultLeafValidity-5*24*time.Hour))
	joined := strings.Join(late.Findings(0), "\n")
	if !strings.Contains(joined, "server certificate ends in") || !strings.Contains(joined, "client certificate ends in") {
		t.Fatalf("a leaf in its renewal window is not reported: %q", joined)
	}
	end := Describe(dir, want(), time.Now().Add(DefaultCAValidity-30*24*time.Hour))
	if !strings.Contains(strings.Join(end.Findings(0), "\n"), "the CA ends in") {
		t.Fatalf("a CA near its end is not reported: %v", end.Findings(0))
	}
}

// The planner is deterministic and a plan applied leaves a state whose plan is empty, for a spread of wants.
func TestPlanThenApplyThenPlanIsEmpty(t *testing.T) {
	for i, w := range []Want{
		{Host: "h"},
		{Host: "h", Names: []string{"a.example", "10.0.0.1", "::1"}},
		{Host: "h", Names: []string{"A.EXAMPLE", "a.example", " "}},
		{Host: "h", ForceCA: true},
		{Host: "h", RenewLeaves: true, LeafValidity: 40 * 24 * time.Hour, RenewBefore: 10 * 24 * time.Hour},
	} {
		dir := filepath.Join(t.TempDir(), fmt.Sprint("tls", i))
		if got, want := Plan(Load(dir), w, time.Now()), Plan(Load(dir), w, time.Now()); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: the plan is not deterministic", i)
		}
		if _, err := Ensure(dir, w, nil); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		w.ForceCA, w.RenewLeaves = false, false
		if steps := Plan(Load(dir), w, time.Now()); len(steps) != 0 {
			t.Fatalf("case %d: the plan after applying is %+v", i, steps)
		}
	}
	_ = ecdsa.PublicKey{}
}
