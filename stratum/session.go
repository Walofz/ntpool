package stratum

import (
	"encoding/json"
	"net"
	"sync"
	"time"

	"ntpool/config"
)

type ShareHistory struct {
	Timestamp int64
	Diff      float64
}

// SessionSnapshot is a value copy of the mutable parts of a session so callers
// outside the session goroutine never read fields while they are being written.
type SessionSnapshot struct {
	ID                    string
	MinerAddress          string
	WorkerName            string
	IsAuthorized          bool
	CurrentDiff           float64
	AcceptedShares        int64
	RejectedShares        int64
	BestShareDiff         float64
	ConnectedAt           time.Time
	VersionRollingEnabled bool
	Status                string
	DisabledReason        string
	BannedReason          string
	Hashrate1m            float64
	Hashrate5m            float64
}

type StratumSession struct {
	mu                    sync.Mutex
	writeMu               sync.Mutex
	ID                    string
	Conn                  net.Conn
	IP                    string
	Extranonce1           string
	Extranonce2Size       int
	IsSubscribed          bool
	IsAuthorized          bool
	MinerAddress          string
	WorkerName            string
	VersionRollingEnabled bool
	VersionRollingMask    string
	PreviousDiff          float64
	CurrentDiff           float64
	AcceptedShares        int64
	RejectedShares        int64
	BestShareDiff         float64
	ConnectedAt           time.Time
	LastShareTime         time.Time
	ShareHistory          []ShareHistory
	LastDiffChangeTime    time.Time
	LastVardiffTime       time.Time
	IsDisabled            bool
	IsBanned              bool
	DisabledReason        string
	BannedReason          string

	dedupOnce sync.Once
	deduper   *shareDeduper
}

func NewStratumSession(id string, conn net.Conn, extranonce1 string, defaultDiff float64) *StratumSession {
	remoteAddr := "127.0.0.1"
	if conn != nil {
		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err == nil {
			remoteAddr = host
		}
	}
	return &StratumSession{
		ID:                 id,
		Conn:               conn,
		IP:                 remoteAddr,
		Extranonce1:        extranonce1,
		Extranonce2Size:    4,
		VersionRollingMask: "1fffe000",
		PreviousDiff:       defaultDiff,
		CurrentDiff:        defaultDiff,
		ConnectedAt:        time.Now(),
		LastDiffChangeTime: time.Now(),
		LastVardiffTime:    time.Now(),
	}
}

func (s *StratumSession) ResetBestShare() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BestShareDiff = 0
}

func (s *StratumSession) Disable(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsDisabled = true
	s.DisabledReason = reason
}

func (s *StratumSession) Ban(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsBanned = true
	s.BannedReason = reason
}

func (s *StratumSession) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsDisabled = false
	s.IsBanned = false
	s.DisabledReason = ""
	s.BannedReason = ""
}

// IsDuplicateShare reports whether the worker already submitted this share.
// The first call for a key returns false and remembers the key.
func (s *StratumSession) IsDuplicateShare(key string) bool {
	s.dedupOnce.Do(func() {
		if s.deduper == nil {
			s.deduper = newShareDeduper(defaultShareCacheSize)
		}
	})
	return s.deduper.SeenOrAdd(key)
}

// SubmitBlocker returns the rejection message for sessions that must not submit shares.
// An empty string means the worker is allowed to submit.
func (s *StratumSession) SubmitBlocker() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case s.IsBanned:
		if s.BannedReason != "" {
			return "Banned: " + s.BannedReason
		}
		return "Banned"
	case s.IsDisabled:
		if s.DisabledReason != "" {
			return "Worker disabled: " + s.DisabledReason
		}
		return "Worker disabled"
	default:
		return ""
	}
}

func (s *StratumSession) GetSessionStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *StratumSession) statusLocked() string {
	switch {
	case s.IsBanned:
		return "banned"
	case s.IsDisabled:
		return "disabled"
	default:
		return "active"
	}
}

// Snapshot returns a consistent copy of the session state.
func (s *StratumSession) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return SessionSnapshot{
		ID:                    s.ID,
		MinerAddress:          s.MinerAddress,
		WorkerName:            s.WorkerName,
		IsAuthorized:          s.IsAuthorized,
		CurrentDiff:           s.CurrentDiff,
		AcceptedShares:        s.AcceptedShares,
		RejectedShares:        s.RejectedShares,
		BestShareDiff:         s.BestShareDiff,
		ConnectedAt:           s.ConnectedAt,
		VersionRollingEnabled: s.VersionRollingEnabled,
		Status:                s.statusLocked(),
		DisabledReason:        s.DisabledReason,
		BannedReason:          s.BannedReason,
		Hashrate1m:            s.hashrateLocked(60),
		Hashrate5m:            s.hashrateLocked(300),
	}
}

// Identity returns the payout address and worker name of the session.
func (s *StratumSession) Identity() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MinerAddress, s.WorkerName
}

// Authorize records the worker identity and marks the session as authorized.
func (s *StratumSession) Authorize(minerAddress, workerName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MinerAddress = minerAddress
	s.WorkerName = workerName
	s.IsAuthorized = true
}

func (s *StratumSession) MarkSubscribed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsSubscribed = true
}

func (s *StratumSession) SetVersionRolling(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.VersionRollingEnabled = enabled
}

func (s *StratumSession) IsAuthorizedNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.IsAuthorized
}

// Diff returns the difficulty currently assigned to the session.
func (s *StratumSession) Diff() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CurrentDiff
}

func (s *StratumSession) RecordReject() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RejectedShares++
}

func (s *StratumSession) RecordShare(cfg *config.Config, targetDiff float64, shareDiff float64) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.AcceptedShares++
	s.LastShareTime = now

	best := shareDiff
	if best <= 0 {
		best = targetDiff
	}
	if best > s.BestShareDiff {
		s.BestShareDiff = best
	}

	s.ShareHistory = append(s.ShareHistory, ShareHistory{Timestamp: now.UnixMilli(), Diff: targetDiff})

	// Keep last 10 minutes of shares
	tenMinAgo := now.Add(-10 * time.Minute).UnixMilli()
	var filtered []ShareHistory
	for _, sh := range s.ShareHistory {
		if sh.Timestamp >= tenMinAgo {
			filtered = append(filtered, sh)
		}
	}
	s.ShareHistory = filtered

	if !cfg.EnableVardiff {
		return 0, false
	}

	if now.Sub(s.LastVardiffTime) >= 45*time.Second && len(s.ShareHistory) >= 10 {
		oldest := s.ShareHistory[0]
		newest := s.ShareHistory[len(s.ShareHistory)-1]
		timeDeltaSec := float64(newest.Timestamp-oldest.Timestamp) / 1000.0

		if timeDeltaSec < 30 {
			return 0, false
		}

		sharesPerMin := (float64(len(s.ShareHistory)) / timeDeltaSec) * 60.0
		targetRate := float64(cfg.VardiffTargetShares)

		ratio := sharesPerMin / targetRate
		if ratio >= 0.5 && ratio <= 2.0 {
			return 0, false
		}

		clampedRatio := mathMax(0.75, mathMin(1.25, ratio))
		newDiff := s.CurrentDiff * clampedRatio

		if newDiff < 64 {
			newDiff = mathMax(1, mathRound(newDiff))
		} else {
			newDiff = mathRound(newDiff/32.0) * 32.0
		}

		if cfg.MinDiff > 0 && newDiff < cfg.MinDiff {
			newDiff = cfg.MinDiff
		}
		if cfg.MaxDiff > 0 && newDiff > cfg.MaxDiff {
			newDiff = cfg.MaxDiff
		}

		if newDiff != s.CurrentDiff {
			s.PreviousDiff = s.CurrentDiff
			s.CurrentDiff = newDiff
			s.LastDiffChangeTime = now
		}
		s.LastVardiffTime = now
		return newDiff, true
	}

	return 0, false
}

func (s *StratumSession) GetHashrate(intervalSec float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hashrateLocked(intervalSec)
}

func (s *StratumSession) hashrateLocked(intervalSec float64) float64 {
	now := time.Now().UnixMilli()
	cutoff := now - int64(intervalSec*1000)

	var totalDiff float64
	for _, sh := range s.ShareHistory {
		if sh.Timestamp >= cutoff {
			totalDiff += sh.Diff
		}
	}

	if totalDiff == 0 || intervalSec <= 0 {
		return 0
	}

	return (totalDiff * 4294967296.0) / intervalSec
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func mathMin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func mathRound(a float64) float64 {
	return float64(int64(a + 0.5))
}

func (s *StratumSession) EffectiveSubmitDiff(gracePeriod time.Duration) (float64, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	required := s.CurrentDiff
	current := s.CurrentDiff

	if s.PreviousDiff > 0 && s.PreviousDiff < s.CurrentDiff {
		if time.Since(s.LastDiffChangeTime) <= gracePeriod {
			required = s.PreviousDiff
		}
	}

	return required, current
}

func (s *StratumSession) WriteJSON(payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.Conn == nil {
		return net.ErrClosed
	}

	_, err = s.Conn.Write(data)
	return err
}
