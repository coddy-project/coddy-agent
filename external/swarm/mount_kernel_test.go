//go:build swarm

package swarm

// The kernel half of the mount's liveness: what the per-call user timeout does
// to a client that really stops answering. The other tests of this package
// prove the cancellation plumbing with connections that return ETIMEDOUT; only
// the kernel can prove that the option makes it happen, and that is this test.
//
// It is opt-in and stays out of `make test`: set CODDY_TEST_NETNS=1 on Linux
// with unshare, ip and iptables installed and unprivileged user namespaces
// allowed. The test re-executes its own binary under `unshare -Urn` (root in a
// fresh user and network namespace), brings loopback up, and makes a client
// vanish with an iptables DROP on the segments sent to its port, so the relay's
// bytes are never acknowledged and nothing ever resets the connection.
//
//	CODDY_TEST_NETNS=1 go test -tags=http,swarm ./external/swarm -run KernelVanishedClient -v
//
// The bound is scaled to B = 8 s and H = 2 s, so U = 6 s. It takes about half a
// minute.

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
)

const (
	netnsParentEnv = "CODDY_TEST_NETNS"
	netnsChildEnv  = "CODDY_TEST_NETNS_CHILD"

	kernelBound     = 8 * time.Second
	kernelHeartbeat = 2 * time.Second
	kernelUserTO    = kernelBound - kernelHeartbeat
)

func TestMountKernelVanishedClient(t *testing.T) {
	if os.Getenv(netnsParentEnv) == "" {
		t.Skip("opt-in kernel test: set CODDY_TEST_NETNS=1 (Linux, unshare, ip and iptables, user namespaces)")
	}
	if os.Getenv(netnsChildEnv) == "" {
		runKernelTestInNetns(t)
		return
	}
	runKernelRows(t)
}

// runKernelTestInNetns re-executes this test under unshare and relays its verdict.
func runKernelTestInNetns(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the kernel test needs Linux")
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is not installed")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(unshare, "-Urn", exe, "-test.run=^TestMountKernelVanishedClient$", "-test.v", "-test.timeout=5m")
	cmd.Env = append(os.Environ(), netnsChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, "--- SKIP: TestMountKernelVanishedClient (") {
		t.Skipf("the namespace is not usable here:\n%s", text)
	}
	if err != nil {
		if strings.Contains(text, "unshare failed") {
			t.Skipf("user namespaces are not allowed here: %v\n%s", err, text)
		}
		t.Fatalf("the kernel test failed in its namespace: %v\n%s", err, text)
	}
	t.Logf("in the namespace:\n%s", text)
}

// iptables runs one iptables command in the namespace.
func iptables(t *testing.T, args ...string) error {
	t.Helper()
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runKernelRows runs inside the namespace.
func runKernelRows(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip is not installed")
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Skipf("cannot bring loopback up: %v: %s", err, out)
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables is not installed")
	}
	if err := iptables(t, "-L", "-n"); err != nil {
		t.Skipf("iptables is not usable here: %v", err)
	}
	requireUserTimeoutSupport(t)
	defer httpx.ScaleLiveness(kernelBound, kernelHeartbeat)()

	t.Run("an HTTP/1.1 client that vanishes frees the node's slot at U after its first unacknowledged byte", func(t *testing.T) {
		stand, client := kernelStand(t)
		port := client.port()
		res := stand.openStream(client.http1())
		defer func() { _ = res.Body.Close() }()
		if stand.node.inUse.Load() != 1 {
			t.Fatalf("the node holds %d calls, want 1", stand.node.inUse.Load())
		}

		vanish := dropToPort(t, port)
		defer vanish.lift()
		vanishedAt := time.Now()

		end, ok := stand.awaitEnded(kernelBound + 6*time.Second)
		if !ok {
			t.Fatalf("the node still held the call %v after the client vanished", time.Since(vanishedAt).Round(time.Millisecond))
		}
		elapsed := end.at.Sub(vanishedAt)
		t.Logf("the node's call ended %v after the client vanished (U = %v, B = %v)", elapsed.Round(10*time.Millisecond), kernelUserTO, kernelBound)
		if !end.cancelled {
			t.Fatal("the node's call ended without its context being cancelled")
		}
		// The first heartbeat written after the vanish is the first unacknowledged byte,
		// at most one node heartbeat later; the kernel aborts U after that.
		if elapsed < kernelUserTO {
			t.Fatalf("the call ended after %v, before U = %v could have run out", elapsed, kernelUserTO)
		}
		if limit := kernelBound + 1500*time.Millisecond; elapsed > limit {
			t.Fatalf("the call ended after %v, later than the bound B = %v (plus 1.5s of timer slack)", elapsed, kernelBound)
		}
		if !stand.node.waitIdle(time.Second) {
			t.Fatal("the node's slot was not released")
		}
	})

	t.Run("an HTTP/2 client that vanishes keeps the node's slot: the documented residual", func(t *testing.T) {
		stand, client := kernelStand(t)
		port := client.port()
		res := stand.openStream(client.http2())
		defer func() { _ = res.Body.Close() }()

		vanish := dropToPort(t, port)
		defer vanish.lift()
		vanishedAt := time.Now()

		// Twice the bound is far beyond where the HTTP/1.1 client was cut; the
		// relay's own kernel gives up only at its retransmission limit, minutes
		// later at the default tcp_retries2.
		if end, ok := stand.awaitEnded(2 * kernelBound); ok {
			t.Fatalf("an HTTP/2 client's call ended %v after it vanished (cancelled=%v): the residual no longer holds, update the documentation",
				end.at.Sub(vanishedAt).Round(time.Second), end.cancelled)
		}
		if stand.node.inUse.Load() != 1 {
			t.Fatalf("the node holds %d calls, want the vanished client's call to be still held", stand.node.inUse.Load())
		}
		vanish.lift()
		_ = res.Body.Close()
		stand.node.releaseAll()
	})

	t.Run("an HTTP/1.1 client that is silent for less than the last retransmission before U is not cut", func(t *testing.T) {
		stand, client := kernelStand(t)
		port := client.port()
		res := stand.openStream(client.http1())
		defer func() { _ = res.Body.Close() }()

		vanish := dropToPort(t, port)
		time.Sleep(2 * time.Second) // retransmissions go out at about 0.2, 0.6, 1.4 and 3.0 s
		vanish.lift()

		// The heartbeats are still coming a few seconds later, and the node never lost the call.
		got := make(chan error, 1)
		go func() {
			br := bufio.NewReader(res.Body)
			for i := 0; i < 3; i++ {
				if _, err := br.ReadString('\n'); err != nil {
					got <- err
					return
				}
			}
			got <- nil
		}()
		select {
		case err := <-got:
			if err != nil {
				t.Fatalf("the stream broke after a 2s outage: %v", err)
			}
		case <-time.After(kernelBound):
			t.Fatal("no heartbeat arrived after a 2s outage")
		}
		select {
		case end := <-stand.node.ended:
			t.Fatalf("the node's call ended (cancelled=%v) though the client was back after 2s", end.cancelled)
		default:
		}
		if stand.node.inUse.Load() != 1 {
			t.Fatalf("the node holds %d calls, want 1", stand.node.inUse.Load())
		}
	})
}

// kernelClient hands out clients of one TLS relay and remembers the local port
// of the connection each opened.
type kernelClient struct {
	stand *liveStand
	mu    sync.Mutex
	local int
}

func (k *kernelClient) note(c net.Conn) {
	if a, ok := c.LocalAddr().(*net.TCPAddr); ok {
		k.mu.Lock()
		k.local = a.Port
		k.mu.Unlock()
	}
}

func (k *kernelClient) http1() *http.Client { return k.stand.h1Client(k.note) }
func (k *kernelClient) http2() *http.Client { return k.stand.h2Client(k.note) }

func (k *kernelClient) port() func() int {
	return func() int {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.local
	}
}

// kernelStand is a TLS relay with a direct node whose heartbeat is shorter than H.
func kernelStand(t *testing.T) (*liveStand, *kernelClient) {
	t.Helper()
	stand := newLiveStand(t, standOptions{tls: true, heartbeat: time.Second})
	return stand, &kernelClient{stand: stand}
}

// vanishing is an iptables rule that drops every segment sent to a client's port.
type vanishing struct {
	t    *testing.T
	args []string
	once sync.Once
}

// dropToPort makes the client with the given local port vanish: whatever the relay
// sends it is lost, so nothing it writes is ever acknowledged.
func dropToPort(t *testing.T, port func() int) *vanishing {
	t.Helper()
	p := port()
	if p == 0 {
		t.Fatal("the client's local port is unknown")
	}
	args := []string{"INPUT", "-p", "tcp", "--dport", strconv.Itoa(p), "-j", "DROP"}
	if err := iptables(t, append([]string{"-I"}, args...)...); err != nil {
		t.Fatal(err)
	}
	return &vanishing{t: t, args: args}
}

// lift lets the segments through again.
func (v *vanishing) lift() {
	v.once.Do(func() {
		if err := iptables(v.t, append([]string{"-D"}, v.args...)...); err != nil {
			v.t.Logf("lifting the rule: %v", err)
		}
	})
}
