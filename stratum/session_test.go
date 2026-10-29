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

	cfg := &config.Config{WalletAddress: "AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr"}
	srv := &StratumServer{
		cfg:        cfg,
		jobManager: pool.NewJobManager(cfg),
		sessions:   map[string]*StratumSession{},
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	sess := NewStratumSession("00000001", server, "00000001", 1024)
	sess.IsAuthorized = true
	if mutate != nil {
		mutate(sess)
	}

	params := []interface{}{"worker", "00000000", "00000000", "65000000", "00000000"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleSubmit(sess, 1, params)
	}()

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("reading stratum response: %v", err)
	}
	<-done

	var resp submitResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decoding stratum response %q: %v", line, err)
	}
	return resp
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
