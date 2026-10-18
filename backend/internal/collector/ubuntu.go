package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/user/lan-dash/internal/db"
)

// UbuntuCollector polls Linux hosts over SSH with one persistent
// connection per device and a single batched command per poll.
type UbuntuCollector struct {
	creds CredentialGetter
	kv    KV

	mu    sync.Mutex
	conns map[int64]*sshState
}

// sshState is the per-device connection + delta-calculation state.
type sshState struct {
	mu       sync.Mutex
	client   *ssh.Client
	prevCPU  cpuSample
	prevNet  map[string]NetStat
	prevTs   time.Time
	backoff  time.Duration
	nextTry  time.Time
	lastErr  error
	hostPort string
}

const (
	minBackoff = 5 * time.Second
	maxBackoff = 5 * time.Minute
)

// NewUbuntuCollector builds the collector.
func NewUbuntuCollector(creds CredentialGetter, kv KV) *UbuntuCollector {
	return &UbuntuCollector{creds: creds, kv: kv, conns: map[int64]*sshState{}}
}

// Type implements Collector.
func (u *UbuntuCollector) Type() string { return "ubuntu" }

// Discover opens a connection (performing TOFU) to validate credentials.
func (u *UbuntuCollector) Discover(ctx context.Context, dev *db.Device) error {
	_, err := u.Poll(ctx, dev)
	return err
}

// Control: monitoring uses a read-only account, so no actions in v1.
func (u *UbuntuCollector) Control(ctx context.Context, dev *db.Device, action Action) error {
	return fmt.Errorf("%w: %q for ubuntu devices", ErrUnsupportedAction, action.Name)
}

// Release closes the persistent connection for a device.
func (u *UbuntuCollector) Release(deviceID int64) {
	u.mu.Lock()
	st := u.conns[deviceID]
	delete(u.conns, deviceID)
	u.mu.Unlock()
	if st != nil {
		st.mu.Lock()
		if st.client != nil {
			st.client.Close()
		}
		st.mu.Unlock()
	}
}

// HostKeyConfigKey is the config-table key holding a TOFU fingerprint.
func HostKeyConfigKey(hostPort string) string { return "ssh_hostkey:" + hostPort }

// ResetHostKey forgets the stored fingerprint so the next connect re-trusts.
func (u *UbuntuCollector) ResetHostKey(ctx context.Context, dev *db.Device) error {
	u.Release(dev.ID)
	return u.kv.DeleteConfig(ctx, HostKeyConfigKey(hostPort(dev)))
}

func hostPort(dev *db.Device) string {
	port := dev.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(dev.Host, strconv.Itoa(port))
}

func (u *UbuntuCollector) state(id int64) *sshState {
	u.mu.Lock()
	defer u.mu.Unlock()
	st, ok := u.conns[id]
	if !ok {
		st = &sshState{prevNet: map[string]NetStat{}}
		u.conns[id] = st
	}
	return st
}

func (st *sshState) fail(err error) error {
	if st.client != nil {
		st.client.Close()
		st.client = nil
	}
	if st.backoff == 0 {
		st.backoff = minBackoff
	} else if st.backoff *= 2; st.backoff > maxBackoff {
		st.backoff = maxBackoff
	}
	st.nextTry = time.Now().Add(st.backoff)
	st.lastErr = err
	return err
}

// Poll implements Collector.
func (u *UbuntuCollector) Poll(ctx context.Context, dev *db.Device) (*Snapshot, error) {
	st := u.state(dev.ID)
	st.mu.Lock()
	defer st.mu.Unlock()

	hp := hostPort(dev)
	if st.client != nil && st.hostPort != hp { // host changed: reconnect
		st.client.Close()
		st.client = nil
	}
	if st.client == nil {
		if time.Now().Before(st.nextTry) && st.lastErr != nil {
			return nil, st.lastErr // still backing off; report last error
		}
		client, err := u.dial(ctx, dev, hp)
		if err != nil {
			return nil, st.fail(err)
		}
		st.client, st.hostPort = client, hp
		st.backoff, st.lastErr = 0, nil
		st.prevCPU, st.prevNet, st.prevTs = cpuSample{}, map[string]NetStat{}, time.Time{}
	}

	out, err := runCommand(ctx, st.client, ubuntuScript)
	if err != nil {
		return nil, st.fail(Unreachable(fmt.Errorf("ssh exec: %w", err)))
	}
	return st.buildSnapshot(out, time.Now())
}

// buildSnapshot parses script output and computes rate-based metrics.
func (st *sshState) buildSnapshot(out []byte, now time.Time) (*Snapshot, error) {
	sec := splitSections(string(out))
	if _, ok := sec["END"]; !ok {
		return nil, errors.New("incomplete output from remote script")
	}
	snap := &Snapshot{Ts: now.Unix()}

	if cs, ok := parseCPU(sec["CPU"]); ok {
		snap.CPUPct = f64(cpuPct(st.prevCPU, cs))
		st.prevCPU = cs
	}
	if l1, l5, l15, ok := parseLoad(sec["LOAD"]); ok {
		snap.Load1, snap.Load5, snap.Load15 = f64(l1), f64(l5), f64(l15)
	}
	if mi := parseMeminfo(sec["MEM"]); mi["MemTotal"] > 0 {
		total := mi["MemTotal"]
		avail, ok := mi["MemAvailable"]
		if !ok { // very old kernels
			avail = mi["MemFree"] + mi["Buffers"] + mi["Cached"]
		}
		used := total - min(avail, total)
		snap.MemTotal, snap.MemUsed = u64(total), u64(used)
		snap.MemPct = f64(clampPct(100 * float64(used) / float64(total)))
		swapTotal := mi["SwapTotal"]
		swapUsed := swapTotal - min(mi["SwapFree"], swapTotal)
		snap.SwapTotal, snap.SwapUsed = u64(swapTotal), u64(swapUsed)
		if swapTotal > 0 {
			snap.SwapPct = f64(clampPct(100 * float64(swapUsed) / float64(swapTotal)))
		} else {
			snap.SwapPct = f64(0)
		}
	}
	snap.Disks = parseDF(sec["DISK"])

	nets := parseNetDev(sec["NET"])
	elapsed := now.Sub(st.prevTs).Seconds()
	for i := range nets {
		if p, ok := st.prevNet[nets[i].Iface]; ok && elapsed > 0 &&
			nets[i].RxBytes >= p.RxBytes && nets[i].TxBytes >= p.TxBytes {
			nets[i].RxBps = float64(nets[i].RxBytes-p.RxBytes) / elapsed
			nets[i].TxBps = float64(nets[i].TxBytes-p.TxBytes) / elapsed
		}
		st.prevNet[nets[i].Iface] = nets[i]
	}
	st.prevTs = now
	snap.Net = nets

	snap.Temps = parseTemps(sec["THERMAL"], sec["SENSORS"])
	if up, ok := parseUptime(sec["UPTIME"]); ok {
		snap.UptimeSecs = f64(up)
	}
	snap.Processes = parseProcs(sec["PROCS"])
	snap.Services = parseServices(sec["SERVICES"])
	snap.Containers = parseDocker(sec["DOCKER"])
	return snap, nil
}

// dial connects with TOFU host-key verification.
func (u *UbuntuCollector) dial(ctx context.Context, dev *db.Device, hp string) (*ssh.Client, error) {
	cred, err := u.creds.Get(ctx, dev.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, errors.New("no SSH credentials configured for this device (Settings → Devices → Credentials)")
		}
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	if cred.Username == "" {
		return nil, errors.New("SSH username not set")
	}
	var auths []ssh.AuthMethod
	if cred.PrivateKey != "" {
		var signer ssh.Signer
		if cred.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cred.PrivateKey), []byte(cred.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(cred.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err) // error text never includes key material
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if cred.Password != "" {
		pw := cred.Password
		auths = append(auths, ssh.Password(pw), ssh.KeyboardInteractive(
			func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = pw
				}
				return ans, nil
			}))
	}
	if len(auths) == 0 {
		return nil, errors.New("credential has neither a private key nor a password")
	}

	// Host-key mismatch is captured here because ssh wraps callback errors
	// as plain strings, which would lose the typed error.
	var mismatch *HostKeyMismatchError
	cfg := &ssh.ClientConfig{
		User:    cred.Username,
		Auth:    auths,
		Timeout: 10 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			k := HostKeyConfigKey(hp)
			stored, ok, err := u.kv.GetConfig(ctx, k)
			if err != nil {
				return fmt.Errorf("host key store: %w", err)
			}
			if !ok { // trust on first use
				return u.kv.SetConfig(ctx, k, fp)
			}
			if stored != fp {
				mismatch = &HostKeyMismatchError{HostPort: hp, Expected: stored, Got: fp}
				return mismatch
			}
			return nil
		},
	}

	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", hp)
	if err != nil {
		return nil, Unreachable(err)
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	c, chans, reqs, err := ssh.NewClientConn(conn, hp, cfg)
	if err != nil {
		conn.Close()
		if mismatch != nil {
			return nil, mismatch
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, Unreachable(err)
		}
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(c, chans, reqs)
	go keepalive(client)
	return client, nil
}

// keepalive detects dead connections between polls.
func keepalive(c *ssh.Client) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
			c.Close()
			return
		}
	}
}

// runCommand executes cmd in a new session, honouring ctx cancellation.
func runCommand(ctx context.Context, c *ssh.Client, cmd string) ([]byte, error) {
	sess, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var stdout bytes.Buffer
	sess.Stdout = &stdout
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case <-ctx.Done():
		sess.Close()
		return nil, ctx.Err()
	case err := <-done:
		// Non-zero exit is fine as long as we got the END marker.
		var exitErr *ssh.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			return nil, err
		}
		return stdout.Bytes(), nil
	}
}
