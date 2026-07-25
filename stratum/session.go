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
