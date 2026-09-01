package web

import "testing"

func TestBuildHealthTimelineEntry(t *testing.T) {
	entry := buildHealthTimelineEntry(true, true, 3, 0)
	if entry["overall"] != "online" {
		t.Fatalf("expected online overall state, got %v", entry["overall"])
	}
	if entry["rpcHealthy"] != true {
		t.Fatalf("expected rpcHealthy=true")
	}
	if entry["connectedWorkers"] != 3 {
		t.Fatalf("expected connectedWorkers=3, got %v", entry["connectedWorkers"])
	}

	degraded := buildHealthTimelineEntry(false, true, 0, 0)
	if degraded["overall"] != "degraded" {
		t.Fatalf("expected degraded overall state, got %v", degraded["overall"])
	}
}

func TestBuildPoolActivityEvent(t *testing.T) {
	event := buildPoolActivityEvent("info", "Workers connected", "3 workers are connected")
	if event["severity"] != "info" {
		t.Fatalf("expected severity info, got %v", event["severity"])
	}
	if event["title"] != "Workers connected" {
		t.Fatalf("expected title Workers connected, got %v", event["title"])
	}
}
