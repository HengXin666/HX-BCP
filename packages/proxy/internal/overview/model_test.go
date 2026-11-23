package overview

import (
	"testing"
	"time"
)

func TestRateUsesSlidingSnapshotDeltas(t *testing.T) {
	previous := Snapshot{Connections: []Connection{{ID: "existing", Upload: 100, Download: 300}}}
	current := Snapshot{Connections: []Connection{
		{ID: "existing", Upload: 140, Download: 380},
		{ID: "new", Upload: 20, Download: 40},
	}}
	upload, download := Rate(previous, current, 2*time.Second)
	if upload != 30 || download != 60 {
		t.Fatalf("Rate() = %d/%d, want 30/60", upload, download)
	}
}

func TestRateIgnoresCounterResetsAndInvalidConnections(t *testing.T) {
	previous := Snapshot{Connections: []Connection{{ID: "reset", Upload: 100, Download: 100}}}
	current := Snapshot{Connections: []Connection{
		{ID: "reset", Upload: 10, Download: 5},
		{ID: "", Upload: 100, Download: 100},
		{ID: "invalid", Upload: -1, Download: 2},
	}}
	upload, download := Rate(previous, current, time.Second)
	if upload != 0 || download != 0 {
		t.Fatalf("Rate() = %d/%d, want 0/0", upload, download)
	}
}
