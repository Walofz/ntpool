package stratum

import (
	"sync"
	"testing"

	"ntpool/config"
	"ntpool/pool"
)

func TestSessionSnapshotTracksState(t *testing.T) {
	sess := NewStratumSession("00000001", nil, "aaaaaaaa", 1024)

	snap := sess.Snapshot()
	if snap.ID != "00000001" {
		t.Fatalf("expected session id 00000001, got %q", snap.ID)
	}
	if snap.Status != "active" || snap.IsAuthorized {
		t.Fatalf("fresh session must be active and unauthorized, got %+v", snap)
	}
	if snap.CurrentDiff != 1024 {
		t.Fatalf("expected default diff 1024, got %v", snap.CurrentDiff)
	}

	sess.Authorize("AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr", "rig1")
	snap = sess.Snapshot()
	if !snap.IsAuthorized || snap.MinerAddress != "AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr" || snap.WorkerName != "rig1" {
		t.Fatalf("authorize must be visible in the snapshot, got %+v", snap)
	}
	if !sess.IsAuthorizedNow() {
		t.Fatal("IsAuthorizedNow must report true after authorize")
	}
	if addr, worker := sess.Identity(); addr != snap.MinerAddress || worker != "rig1" {
		t.Fatalf("identity mismatch: %q / %q", addr, worker)
	}

	sess.RecordReject()
	sess.RecordReject()
	if snap = sess.Snapshot(); snap.RejectedShares != 2 {
		t.Fatalf("expected 2 rejected shares, got %d", snap.RejectedShares)
	}

	sess.SetVersionRolling(true)
	if snap = sess.Snapshot(); !snap.VersionRollingEnabled {
		t.Fatal("version rolling must be reported in the snapshot")
	}

	sess.Disable("abuse")
	snap = sess.Snapshot()
	if snap.Status != "disabled" || snap.DisabledReason != "abuse" {
		t.Fatalf("disabled state must be reported, got %+v", snap)
	}

	sess.Ban("bad shares")
	if snap = sess.Snapshot(); snap.Status != "banned" || snap.BannedReason != "bad shares" {
		t.Fatalf("banned state must be reported, got %+v", snap)
	}

	sess.Resume()
	if snap = sess.Snapshot(); snap.Status != "active" || snap.DisabledReason != "" || snap.BannedReason != "" {
		t.Fatalf("resume must clear the ban/disable flags, got %+v", snap)
	}

	if diff := sess.Diff(); diff != 1024 {
		t.Fatalf("expected diff 1024, got %v", diff)
	}
}

func TestSessionSnapshotIsIndependentCopy(t *testing.T) {
	sess := NewStratumSession("00000002", nil, "bbbbbbbb", 512)
	sess.Authorize("AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr", "rig1")
	sess.RecordReject()

	before := sess.Snapshot()

	sess.Authorize("AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr", "rig2")
	sess.RecordReject()
	sess.SetVersionRolling(true)

	if before.WorkerName != "rig1" || before.RejectedShares != 1 || before.VersionRollingEnabled {
		t.Fatalf("earlier snapshot must not change after the session is mutated, got %+v", before)
	}
}

// TestSessionSnapshotConcurrentAccess guards the web dashboard against reading
// session state while the session goroutine mutates it (run with -race).
func TestSessionSnapshotConcurrentAccess(t *testing.T) {
	cfg := &config.Config{DefaultDiff: 1024}
	srv := &StratumServer{
		cfg:        cfg,
		jobManager: pool.NewJobManager(cfg),
		sessions:   map[string]*StratumSession{},
		stopChan:   make(chan struct{}),
	}

	sess := NewStratumSession("00000003", nil, "cccccccc", 1024)
	srv.sessions[sess.ID] = sess

	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			sess.Authorize("AWPuDcCymof8BRF9cfkxnLqmhn7ZPVPjEr", "rig")
			sess.MarkSubscribed()
			sess.SetVersionRolling(true)
			sess.RecordReject()
			sess.RecordShare(cfg, 1024, 4096)
			sess.Disable("flood")
			sess.Resume()
			sess.Ban("abuse")
			sess.Resume()
		}
		close(done)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			for _, snap := range srv.GetSessionSnapshots() {
				if snap.ID != "00000003" {
					t.Errorf("unexpected session id %q", snap.ID)
				}
				_ = snap.Status == "active" && snap.CurrentDiff >= 0
			}
			_ = sess.Diff()
			_ = sess.IsAuthorizedNow()
			_ = sess.GetSessionStatus()
			_ = sess.GetHashrate(60)
			_, _ = sess.Identity()
			select {
			case <-done:
				return
			default:
			}
		}
	}()

	wg.Wait()
}
