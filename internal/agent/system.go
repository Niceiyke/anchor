package agent

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// statsReporter periodically samples and reports system stats (Linux).
func (a *Agent) statsReporter(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		a.emit(protocol.EvtSystemStats, sampleStats())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func sampleStats() protocol.SystemStats {
	var s protocol.SystemStats
	s.CPUPercent = cpuPercent()
	s.MemUsed, s.MemTotal = memInfo()
	s.DiskUsed, s.DiskTotal = diskInfo("/")
	s.LoadAvg = loadAvg()
	s.UptimeSecs = uptime()
	s.Containers = containerCount()
	return s
}

// cpuPercent samples /proc/stat over a short window.
func cpuPercent() float64 {
	idle0, total0 := cpuSample()
	time.Sleep(200 * time.Millisecond)
	idle1, total1 := cpuSample()
	dt := total1 - total0
	if dt == 0 {
		return 0
	}
	return 100 * (1 - float64(idle1-idle0)/float64(dt))
}

func cpuSample() (idle, total uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		for i, f := range fields {
			v, _ := strconv.ParseUint(f, 10, 64)
			total += v
			if i == 3 { // idle
				idle = v
			}
		}
		break
	}
	return idle, total
}

func memInfo() (used, total uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var memTotal, memAvail uint64
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			memTotal = v * 1024
		case "MemAvailable:":
			memAvail = v * 1024
		}
	}
	if memTotal == 0 {
		return 0, 0
	}
	return memTotal - memAvail, memTotal
}

func diskInfo(path string) (used, total uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, 0
	}
	total = st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	return total - free, total
}

func loadAvg() [3]float64 {
	var la [3]float64
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return la
	}
	fields := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(fields); i++ {
		la[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return la
}

func uptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func containerCount() int {
	out, err := exec.Command("docker", "ps", "-q").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
