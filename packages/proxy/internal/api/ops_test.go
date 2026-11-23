package api

import (
	"testing"
)

func TestParseDfOutput(t *testing.T) {
	output := `Filesystem     1024-blocks        Used Available Capacity Mounted on
overlay       1059968000   581132032 435732224      58% /
tmpfs            1994344         168   1994176       1% /dev
/dev/vda1     10068836864  9000000000 1068836864      89% /root/data
/dev/vda2       209715200    10000000 199715200       5% /boot
proc                  0           0         0        - /proc
`
	rows, err := parseDfOutput(output)
	if err != nil {
		t.Fatalf("parseDfOutput returned error: %v", err)
	}
	// overlay/tmpfs/proc must be filtered; only the two real disks remain.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after filtering, got %d: %+v", len(rows), rows)
	}
	root := rows[0]
	if root.MountedOn != "/root/data" {
		t.Errorf("expected first row mounted on /root/data, got %q", root.MountedOn)
	}
	if root.Filesystem != "/dev/vda1" {
		t.Errorf("expected filesystem /dev/vda1, got %q", root.Filesystem)
	}
	if root.UsePercent != 89 {
		t.Errorf("expected use_percent 89, got %d", root.UsePercent)
	}
	if root.AvailBytes != 1068836864 {
		t.Errorf("expected avail 1068836864, got %d", root.AvailBytes)
	}
}

func TestParseDockerPSAndMergeStats(t *testing.T) {
	psOutput := `{"ID":"abc123def456","Names":"web-app","Image":"nginx:latest","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:8080->80/tcp","CreatedAt":"2026-08-23 05:00:00 +0000 UTC"}
{"ID":"def456abc789","Names":"db","Image":"postgres:16","State":"exited","Status":"Exited (0) 2 days ago","Ports":"","CreatedAt":"2026-08-21 05:00:00 +0000 UTC"}
`
	containers, err := parseDockerPS(psOutput)
	if err != nil {
		t.Fatalf("parseDockerPS returned error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].Name != "web-app" || containers[0].Ports != "0.0.0.0:8080->80/tcp" {
		t.Errorf("unexpected first container: %+v", containers[0])
	}

	statsOutput := `{"Container":"abc123def456","CPUPerc":"1.23%","MemUsage":"15.3MiB / 7.76GiB","MemPerc":"0.19%","NetIO":"1.2kB / 3.4kB","BlockIO":"0B / 0B"}
`
	mergeDockerStats(containers, statsOutput)
	if containers[0].CPUPerc != "1.23%" {
		t.Errorf("expected cpu 1.23%%, got %q", containers[0].CPUPerc)
	}
	if containers[0].MemUsage != "15.3MiB / 7.76GiB" {
		t.Errorf("expected mem usage, got %q", containers[0].MemUsage)
	}
	// The exited container must remain untouched.
	if containers[1].CPUPerc != "" {
		t.Errorf("exited container should have no stats, got %q", containers[1].CPUPerc)
	}
}

func TestParseDfOutputEmpty(t *testing.T) {
	rows, err := parseDfOutput("")
	if err != nil {
		t.Fatalf("empty output should not error, got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows for empty output, got %d", len(rows))
	}
}
