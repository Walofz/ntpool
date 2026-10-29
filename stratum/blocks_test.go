package stratum

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newBlocksTestServer(t *testing.T) *StratumServer {
	t.Helper()
	return &StratumServer{blocksFilePath: filepath.Join(t.TempDir(), "data", "found_blocks.json")}
}

func TestRecordFoundBlockPersistsNewestFirst(t *testing.T) {
	srv := newBlocksTestServer(t)

	srv.recordFoundBlock(FoundBlock{Height: 1, Hash: "aa"})
	srv.recordFoundBlock(FoundBlock{Height: 2, Hash: "bb"})

	blocks := srv.GetFoundBlocks()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks in memory, got %d", len(blocks))
	}
	if blocks[0].Height != 2 || blocks[1].Height != 1 {
		t.Fatalf("expected newest block first, got %+v", blocks)
	}

	data, err := os.ReadFile(srv.blocksFilePath)
	if err != nil {
		t.Fatalf("reading persisted blocks: %v", err)
	}
	var persisted []FoundBlock
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decoding persisted blocks: %v", err)
	}
	if len(persisted) != 2 || persisted[0].Height != 2 {
		t.Fatalf("persisted blocks must match memory, got %+v", persisted)
	}
}

func TestGetFoundBlocksReturnsCopy(t *testing.T) {
	srv := newBlocksTestServer(t)
	srv.recordFoundBlock(FoundBlock{Height: 7, Hash: "cc"})

	blocks := srv.GetFoundBlocks()
	blocks[0].Height = 999

	if got := srv.GetFoundBlocks()[0].Height; got != 7 {
		t.Fatalf("GetFoundBlocks must not expose internal state, height changed to %d", got)
	}
}

func TestTrimFoundBlocksCapsHistory(t *testing.T) {
	blocks := make([]FoundBlock, maxFoundBlocks+25)
	for i := range blocks {
		blocks[i] = FoundBlock{Height: int64(i)}
	}

	trimmed := trimFoundBlocks(blocks)
	if len(trimmed) != maxFoundBlocks {
		t.Fatalf("expected history capped at %d, got %d", maxFoundBlocks, len(trimmed))
	}
	if trimmed[0].Height != 0 {
		t.Fatalf("trimming must keep the newest entries, first height is %d", trimmed[0].Height)
	}
}

func TestLoadFoundBlocksCapsPersistedHistory(t *testing.T) {
	srv := newBlocksTestServer(t)

	oversized := make([]FoundBlock, maxFoundBlocks+10)
	for i := range oversized {
		oversized[i] = FoundBlock{Height: int64(i)}
	}
	data, err := json.Marshal(oversized)
	if err != nil {
		t.Fatalf("encoding fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(srv.blocksFilePath), 0755); err != nil {
		t.Fatalf("creating data dir: %v", err)
	}
	if err := os.WriteFile(srv.blocksFilePath, data, 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	srv.loadFoundBlocks()

	if got := len(srv.GetFoundBlocks()); got != maxFoundBlocks {
		t.Fatalf("expected loaded history capped at %d, got %d", maxFoundBlocks, got)
	}
}

func TestFoundBlocksConcurrentAccess(t *testing.T) {
	srv := newBlocksTestServer(t)

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				srv.recordFoundBlock(FoundBlock{Height: int64(worker*100 + i)})
				_ = srv.GetFoundBlocks()
			}
		}(worker)
	}
	wg.Wait()

	if got := len(srv.GetFoundBlocks()); got != 200 {
		t.Fatalf("expected 200 recorded blocks, got %d", got)
	}

	srv.saveFoundBlocks()

	data, err := os.ReadFile(srv.blocksFilePath)
	if err != nil {
		t.Fatalf("reading persisted blocks: %v", err)
	}
	var persisted []FoundBlock
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted file is not valid JSON after concurrent saves: %v", err)
	}
	if len(persisted) != 200 {
		t.Fatalf("expected 200 persisted blocks, got %d", len(persisted))
	}
}
