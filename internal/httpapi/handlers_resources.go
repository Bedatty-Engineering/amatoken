package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ResourceStats struct {
	Goroutines       int     `json:"goroutines"`
	MemoryMB         int64   `json:"memoryMB"`         // container RSS in MB
	MemoryPctHost    float64 `json:"memory_pct_host"`  // container memory / host total × 100
	CPUPctHost       float64 `json:"cpu_pct_host"`     // 0..(100*ncpu) — % of one core; multi-core can exceed 100
	HostMemTotalMB   int64   `json:"host_memory_total_mb"`
	HostCPUCount     int     `json:"host_cpu_count"`
}

// cpuSampler is a tiny rolling sampler over the cgroup cpu.stat counter.
// Two consecutive reads (delta of usage µs over wall clock) give an interval
// CPU percentage that's scaled against (nproc × interval) for "% of host CPU".
type cpuSampler struct {
	mu       sync.Mutex
	lastUsec uint64
	lastAt   time.Time
}

var cpuState = &cpuSampler{}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	stats := ResourceStats{
		Goroutines:   runtime.NumGoroutine(),
		HostCPUCount: runtime.NumCPU(),
	}

	// Container memory (RSS-ish from cgroup, falls back to Go heap if unreadable).
	if mem, err := readContainerMemory(); err == nil {
		stats.MemoryMB = int64(mem / (1024 * 1024))
		if hostMem, err2 := readHostMemTotal(); err2 == nil && hostMem > 0 {
			stats.HostMemTotalMB = int64(hostMem / (1024 * 1024))
			stats.MemoryPctHost = float64(mem) / float64(hostMem) * 100
		}
	} else {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		stats.MemoryMB = int64(m.Alloc / 1024 / 1024)
	}

	stats.CPUPctHost = cpuState.sample()

	writeJSON(w, 200, stats)
}

// sample returns CPU usage between the previous call and now, expressed as
// percent of HOST CPU capacity (i.e. averaged across all cores). 0% on the
// very first call (no baseline yet) is the expected behaviour.
func (s *cpuSampler) sample() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := readCgroupCPUUsec()
	if err != nil {
		return 0
	}
	now := time.Now()
	if s.lastAt.IsZero() {
		s.lastUsec = cur
		s.lastAt = now
		return 0
	}
	deltaUsec := float64(cur - s.lastUsec)
	deltaSec := now.Sub(s.lastAt).Seconds()
	s.lastUsec = cur
	s.lastAt = now
	if deltaSec <= 0 {
		return 0
	}
	cores := float64(runtime.NumCPU())
	if cores <= 0 {
		cores = 1
	}
	// Available CPU-µs in the interval = deltaSec × 1e6 × cores.
	return deltaUsec / (deltaSec * 1e6 * cores) * 100
}

func readCgroupCPUUsec() (uint64, error) {
	// cgroup v2
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "usage_usec ") {
				return strconv.ParseUint(strings.TrimSpace(line[len("usage_usec "):]), 10, 64)
			}
		}
	}
	// cgroup v1: counter is in nanoseconds — convert to µs for parity.
	if data, err := os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage"); err == nil {
		ns, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err == nil {
			return ns / 1000, nil
		}
	}
	return 0, fmt.Errorf("cpu usage counter not found")
}

func readContainerMemory() (uint64, error) {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.current",                  // v2
		"/sys/fs/cgroup/memory/memory.usage_in_bytes",   // v1
	} {
		if data, err := os.ReadFile(path); err == nil {
			return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		}
	}
	return 0, fmt.Errorf("memory cgroup not readable")
}

func readHostMemTotal() (uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return kb * 1024, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("MemTotal not found")
}
