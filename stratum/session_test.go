package stratum

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"ntpool/config"
	"ntpool/pool"
)

func TestSubmitBlocker(t *testing.T) {
	sess := NewStratumSession("00000001", nil, "00000001", 1024)

	if msg := sess.SubmitBlocker(); msg != "" {
		t.Fatalf("fresh session must be allowed to submit, got %q", msg)
	}

	sess.Disable("abuse")
	if msg := sess.SubmitBlocker(); !strings.Contains(msg, "disabled") || !strings.Contains(msg, "abuse") {
		t.Fatalf("disabled session must be blocked with its reason, got %q", msg)
	}

	sess.Resume()
	if msg := sess.SubmitBlocker(); msg != "" {
		t.Fatalf("resumed session must be allowed to submit, got %q", msg)
	}

	sess.Ban("bad shares")
	if msg := sess.SubmitBlocker(); !strings.Contains(msg, "Banned") || !strings.Contains(msg, "bad shares") {
		t.Fatalf("banned session must be blocked with its reason, got %q", msg)
	}
}

type submitResponse struct {
	ID     interface{} `json:"id"`
	Result bool        `json:"result"`
	Error  struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func submitOnce(t *testing.T, mutate func(*StratumSession)) submitResponse {
	t.Helper()
	return newSubmitEnv(t, mutate).submit(defaultShareParams())
}

func defaultShareParams() []interface{} {
	return []interface{}{"worker", "00000000", "00000000", "65000000", "00000000"}
}

type submitEnv struct {
	t      *testing.T
	srv    *StratumServer
	sess   *StratumSession
	client net.Conn
	reader *bufio.Reader
}

func newSubmitEnv(t *testing.T, mutate func(*StratumSession)) *submitEnv {
	t.Helper()

	cfg := &config.Config{WalletAddress: "AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr"}
	srv := &StratumServer{
		cfg:        cfg,
		jobManager: pool.NewJobManager(cfg),
		sessions:   map[string]*StratumSession{},
	}

	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	sess := NewStratumSession("00000001", server, "00000001", 1024)
	sess.IsAuthorized = true
	if mutate != nil {
		mutate(sess)
	}

	return &submitEnv{t: t, srv: srv, sess: sess, client: client, reader: bufio.NewReader(client)}
}

func (e *submitEnv) submit(params []interface{}) submitResponse {
	e.t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.handleSubmit(e.sess, 1, params)
	}()

	_ = e.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := e.reader.ReadString('\n')
	if err != nil {
		e.t.Fatalf("reading stratum response: %v", err)
	}
	<-done

	var resp submitResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		e.t.Fatalf("decoding stratum response %q: %v", line, err)
	}
	return resp
}

func TestShareDeduperEvictsOldest(t *testing.T) {
	deduper := newShareDeduper(3)

	for _, key := range []string{"a", "b", "c"} {
		if deduper.SeenOrAdd(key) {
			t.Fatalf("%q reported as duplicate before its first use", key)
		}
	}
	if !deduper.SeenOrAdd("b") {
		t.Fatalf("repeat of %q must be reported as duplicate", "b")
	}
	if deduper.SeenOrAdd("d") {
		t.Fatalf("new key %q must not be a duplicate", "d")
	}
	if deduper.SeenOrAdd("a") {
		t.Fatalf("oldest key %q should have been evicted", "a")
	}
}

func TestHandleSubmitRejectsDuplicateShare(t *testing.T) {
	env := newSubmitEnv(t, nil)

	job := env.srv.jobManager.CreateJob(map[string]interface{}{
		"height":            float64(1000),
		"coinbasevalue":     float64(5000000000),
		"previousblockhash": strings.Repeat("00", 32),
		"version":           float64(536870912),
		"bits":              "207fffff",
		"curtime":           float64(1700000000),
	})

	params := []interface{}{"worker", job.JobId, "00000000", job.NTimeHex, "00000000"}
	first := env.submit(params)
	if first.Result || !strings.Contains(first.Error.Message, "Low difficulty") {
		t.Fatalf("first submission must be processed normally, got %+v", first)
	}

	second := env.submit(params)
	if second.Result || second.Error.Message != "Duplicate share" {
		t.Fatalf("resubmitted share must be rejected as duplicate, got %+v", second)
	}

	other := env.submit([]interface{}{"worker", job.JobId, "00000000", job.NTimeHex, "00000001"})
	if other.Error.Message == "Duplicate share" {
		t.Fatalf("share with a different nonce must not be treated as duplicate")
	}
}

func TestHandleSubmitRejectsBlockedWorker(t *testing.T) {
	disabled := submitOnce(t, func(s *StratumSession) { s.Disable("misbehaving") })
	if disabled.Result || !strings.Contains(disabled.Error.Message, "disabled") {
		t.Fatalf("disabled worker share must be rejected, got %+v", disabled)
	}

	banned := submitOnce(t, func(s *StratumSession) { s.Ban("spam") })
	if banned.Result || !strings.Contains(banned.Error.Message, "Banned") {
		t.Fatalf("banned worker share must be rejected, got %+v", banned)
	}

	active := submitOnce(t, nil)
	if active.Result || !strings.Contains(active.Error.Message, "Stale") {
		t.Fatalf("active worker must still reach the job lookup path, got %+v", active)
	}
}

func seedShares(sess *StratumSession, shares int, spanSeconds float64) {
	now := time.Now()
	for i := 0; i < shares; i++ {
		offset := spanSeconds * float64(shares-1-i) / float64(shares-1)
		sess.ShareHistory = append(sess.ShareHistory, ShareHistory{
			Timestamp: now.Add(-time.Duration(offset * float64(time.Second))).UnixMilli(),
			Diff:      1,
		})
	}
	sess.LastVardiffTime = now.Add(-time.Minute)
}

func newVardiffConfig(minDiff, maxDiff float64) *config.Config {
	return &config.Config{
		EnableVardiff:        true,
		VardiffTargetShares:  12,
		MinDiff:              minDiff,
		MaxDiff:              maxDiff,
	}
}

func TestVardiffHonorsMinDiff(t *testing.T) {
	sess := NewStratumSession("s1", nil, "00000001", 1024)
	sess.CurrentDiff = 1024
	sess.PreviousDiff = 1024
	seedShares(sess, 10, 200) // ~3 shares/min -> vardiff ลด diff

	newDiff, changed := sess.RecordShare(newVardiffConfig(1024, 1048576), 1024, 1)
	if !changed {
		t.Fatalf("expected vardiff to adjust difficulty")
	}
	if newDiff != 1024 || sess.CurrentDiff != 1024 {
		t.Fatalf("expected difficulty clamped to MinDiff 1024, got %v (current %v)", newDiff, sess.CurrentDiff)
	}
}

func TestVardiffHonorsMaxDiff(t *testing.T) {
	sess := NewStratumSession("s1", nil, "00000001", 1048576)
	sess.CurrentDiff = 1048576
	sess.PreviousDiff = 1048576
	seedShares(sess, 30, 40) // ~46 shares/min -> vardiff เพิ่ม diff

	newDiff, changed := sess.RecordShare(newVardiffConfig(64, 1048576), 1048576, 1)
	if !changed {
		t.Fatalf("expected vardiff to adjust difficulty")
	}
	if newDiff != 1048576 || sess.CurrentDiff != 1048576 {
		t.Fatalf("expected difficulty clamped to MaxDiff 1048576, got %v (current %v)", newDiff, sess.CurrentDiff)
	}
}

func TestVardiffWithoutBoundsStillAdjusts(t *testing.T) {
	sess := NewStratumSession("s1", nil, "00000001", 1024)
	sess.CurrentDiff = 1024
	sess.PreviousDiff = 1024
	seedShares(sess, 10, 200)

	newDiff, changed := sess.RecordShare(newVardiffConfig(0, 0), 1024, 1)
	if !changed {
		t.Fatalf("expected vardiff to adjust difficulty")
	}
	if newDiff != 768 {
		t.Fatalf("expected unclamped ratio step down to 768, got %v", newDiff)
	}
}

func TestIsSubmitBlockAccepted(t *testing.T) {
	srv := &StratumServer{}

	cases := []struct {
		name   string
		result interface{}
		err    error
		want   bool
	}{
		{"null result", nil, nil, true},
		{"empty result", "", nil, true},
		{"duplicate", "duplicate", nil, false},
		{"bad block", "bad-witness-nonce-size", nil, false},
		{"rpc error", nil, errors.New("connection refused"), false},
	}

	for _, c := range cases {
		if got := srv.isSubmitBlockAccepted(c.result, c.err); got != c.want {
			t.Fatalf("%s: expected %v, got %v", c.name, c.want, got)
		}
	}
}
