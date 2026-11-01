package monitoring

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	content := "MemTotal:       1000 kB\nMemFree:         200 kB\nMemAvailable:    400 kB\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalBytes != 1_024_000 || got.AvailableBytes != 409_600 || got.UsedBytes != 614_400 || got.Percent != 60 {
		t.Fatalf("unexpected memory values: %+v", got)
	}
}

func TestReadCPUTicks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, []byte("cpu  10 2 3 40 5 1 1 0 0 0\ncpu0 1 0 1 10 0 0 0 0 0 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCPUTicks(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.total != 62 || got.idle != 45 {
		t.Fatalf("unexpected CPU counters: %+v", got)
	}
}

func TestReadNetworkIgnoresLoopback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev")
	content := "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\nlo: 999 0 0 0 0 0 0 0 888 0 0 0 0 0 0 0\neth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	rx, tx, err := readNetwork(path)
	if err != nil {
		t.Fatal(err)
	}
	if rx != 100 || tx != 200 {
		t.Fatalf("unexpected network counters: %d / %d", rx, tx)
	}
}

func TestDisksDiscoversPhysicalDeviceAndSkipsLoop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sys")
	for _, name := range []string{"sda", "loop0"} {
		device := filepath.Join(root, "class", "block", name, "device")
		if err := os.MkdirAll(device, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "class", "block", name, "size"), []byte("2048\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "class", "block", name, "removable"), []byte("0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(device, "model"), []byte("Example Disk\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(device, "serial"), []byte("serial-123\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	disks, err := New("", root).Disks()
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].Name != "sda" || disks[0].Capacity != 1_048_576 ||
		disks[0].Model != "Example Disk" || disks[0].Serial != "serial-123" {
		t.Fatalf("unexpected discovered disks: %+v", disks)
	}
}

func TestSystemSamplesLiveCPUAndNetworkCounters(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(proc, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\n")
	write("uptime", "1234.5 0\n")
	write("stat", "cpu 10 0 0 90 0 0 0 0 0 0\n")
	write("net/dev", "Inter-| Receive | Transmit\neth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n")
	system := New(proc, filepath.Join(root, "sys"))
	if err := system.sample(); err != nil {
		t.Fatal(err)
	}
	write("stat", "cpu 20 0 0 180 0 0 0 0 0 0\n")
	write("net/dev", "Inter-| Receive | Transmit\neth0: 200 0 0 0 0 0 0 0 400 0 0 0 0 0 0 0\n")
	if err := system.sample(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := system.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CPUPercent == nil || math.Abs(*snapshot.CPUPercent-10) > 1e-9 || snapshot.Memory.Percent != 60 ||
		snapshot.UptimeSeconds != 1234.5 || snapshot.Network.ReceiveBytesPerSecond <= 0 ||
		snapshot.Network.SendBytesPerSecond <= 0 {
		t.Fatalf("unexpected sampled system metrics: %+v", snapshot)
	}
}
