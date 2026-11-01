package monitoring

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Memory struct {
	TotalBytes     uint64  `json:"totalBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	AvailableBytes uint64  `json:"availableBytes"`
	Percent        float64 `json:"percent"`
}

type Network struct {
	ReceiveBytesPerSecond float64 `json:"receiveBytesPerSecond"`
	SendBytesPerSecond    float64 `json:"sendBytesPerSecond"`
}

type Snapshot struct {
	CPUPercent    *float64  `json:"cpuPercent"`
	Memory        Memory    `json:"memory"`
	Network       Network   `json:"network"`
	UptimeSeconds float64   `json:"uptimeSeconds"`
	CollectedAt   time.Time `json:"collectedAt"`
}

type Disk struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Serial    string `json:"serial"`
	Capacity  uint64 `json:"capacityBytes"`
	Removable bool   `json:"removable"`
}

type cpuTicks struct {
	total uint64
	idle  uint64
}

type counters struct {
	received uint64
	sent     uint64
	at       time.Time
}

type System struct {
	procRoot string
	sysRoot  string
	mu       sync.RWMutex
	cpu      *float64
	lastCPU  *cpuTicks
	lastNet  *counters
	network  Network
}

func New(procRoot, sysRoot string) *System {
	return &System{procRoot: procRoot, sysRoot: sysRoot}
}

func (s *System) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := s.sample(); err != nil {
				fmt.Fprintf(os.Stderr, "ASNS monitoring sample failed: %v\n", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *System) Snapshot() (Snapshot, error) {
	memory, err := readMemory(filepath.Join(s.procRoot, "meminfo"))
	if err != nil {
		return Snapshot{}, err
	}
	uptime, err := readUptime(filepath.Join(s.procRoot, "uptime"))
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		CPUPercent: s.cpu, Memory: memory, Network: s.network,
		UptimeSeconds: uptime, CollectedAt: time.Now().UTC(),
	}, nil
}

func (s *System) Disks() ([]Disk, error) {
	root := filepath.Join(s.sysRoot, "class", "block")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read block devices: %w", err)
	}
	disks := make([]Disk, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "fd") {
			continue
		}
		devicePath := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(devicePath, "device")); err != nil {
			continue
		}
		sectors, err := readUint(filepath.Join(devicePath, "size"))
		if err != nil {
			return nil, fmt.Errorf("read capacity for %s: %w", name, err)
		}
		model, err := readOptional(filepath.Join(devicePath, "device", "model"))
		if err != nil {
			return nil, fmt.Errorf("read model for %s: %w", name, err)
		}
		serial, err := readOptional(filepath.Join(devicePath, "device", "serial"))
		if err != nil {
			return nil, fmt.Errorf("read serial for %s: %w", name, err)
		}
		removable, err := readUint(filepath.Join(devicePath, "removable"))
		if err != nil {
			return nil, fmt.Errorf("read removable flag for %s: %w", name, err)
		}
		disks = append(disks, Disk{
			Name: name, Model: model, Serial: serial, Capacity: sectors * 512, Removable: removable != 0,
		})
	}
	return disks, nil
}

func (s *System) sample() error {
	ticks, err := readCPUTicks(filepath.Join(s.procRoot, "stat"))
	if err != nil {
		return err
	}
	received, sent, err := readNetwork(filepath.Join(s.procRoot, "net", "dev"))
	if err != nil {
		return err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastCPU != nil && ticks.total > s.lastCPU.total {
		totalDelta := ticks.total - s.lastCPU.total
		idleDelta := ticks.idle - s.lastCPU.idle
		percent := 100 * (1 - float64(idleDelta)/float64(totalDelta))
		percent = math.Max(0, math.Min(100, percent))
		s.cpu = &percent
	}
	s.lastCPU = &ticks
	if s.lastNet != nil && now.After(s.lastNet.at) && received >= s.lastNet.received && sent >= s.lastNet.sent {
		elapsed := now.Sub(s.lastNet.at).Seconds()
		s.network = Network{
			ReceiveBytesPerSecond: float64(received-s.lastNet.received) / elapsed,
			SendBytesPerSecond:    float64(sent-s.lastNet.sent) / elapsed,
		}
	}
	s.lastNet = &counters{received: received, sent: sent, at: now}
	return nil
}

func readCPUTicks(path string) (cpuTicks, error) {
	file, err := os.Open(path)
	if err != nil {
		return cpuTicks{}, fmt.Errorf("read CPU counters: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return cpuTicks{}, err
		}
		return cpuTicks{}, errors.New("CPU counters are empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTicks{}, errors.New("invalid CPU counters in procfs")
	}
	var result cpuTicks
	for index, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuTicks{}, fmt.Errorf("parse CPU counter: %w", err)
		}
		if index < 8 {
			result.total += value
		}
		if index == 3 || index == 4 {
			result.idle += value
		}
	}
	return result, nil
}

func readMemory(path string) (Memory, error) {
	file, err := os.Open(path)
	if err != nil {
		return Memory{}, fmt.Errorf("read memory information: %w", err)
	}
	defer file.Close()
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return Memory{}, fmt.Errorf("parse memory value: %w", err)
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return Memory{}, fmt.Errorf("scan memory information: %w", err)
	}
	total, hasTotal := values["MemTotal"]
	available, hasAvailable := values["MemAvailable"]
	if !hasTotal || !hasAvailable || total == 0 || available > total {
		return Memory{}, errors.New("memory information is missing required values")
	}
	used := total - available
	return Memory{TotalBytes: total, UsedBytes: used, AvailableBytes: available, Percent: 100 * float64(used) / float64(total)}, nil
}

func readUptime(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read uptime: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("uptime information is empty")
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse uptime: %w", err)
	}
	return uptime, nil
}

func readNetwork(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read network counters: %w", err)
	}
	defer file.Close()
	var received, sent uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		separator := strings.IndexByte(line, ':')
		if separator < 0 {
			continue
		}
		name := strings.TrimSpace(line[:separator])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(line[separator+1:])
		if len(fields) < 9 {
			return 0, 0, fmt.Errorf("invalid network counters for %s", name)
		}
		rx, rxErr := strconv.ParseUint(fields[0], 10, 64)
		tx, txErr := strconv.ParseUint(fields[8], 10, 64)
		if rxErr != nil || txErr != nil {
			return 0, 0, fmt.Errorf("parse network counters for %s", name)
		}
		received += rx
		sent += tx
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan network counters: %w", err)
	}
	return received, sent, nil
}

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
