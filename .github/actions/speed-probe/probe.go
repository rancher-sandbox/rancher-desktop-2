// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// probe times fixed work on the runner, so that a BATS timing can be
// compared across runs on machines of different speeds. It prints each
// result as an "rdd-perf:" line, in the same format as the lines that
// rdd/scripts/metrics.bash prints for rdd's own records. It doesn't use
// any rdd code, so a change to rdd can't move its numbers.
//
// Usage:
//
//	go run .github/actions/speed-probe/probe.go --when before|after [--dir DIR]
package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// These constants size the probe's work so that a whole run stays under
// 20 s on the slowest CI runner, the Intel macOS one, which hashes several
// times slower than the others. The hash, memory and disk workloads each
// reuse their buffers for every pass, so the probe's memory doesn't grow
// with the amount of work.
const (
	rounds       = 5
	hashSize     = 32 << 20
	hashRepeats  = 4
	memSize      = 64 << 20 // larger than the CPU caches
	memRepeats   = 96
	diskSize     = 256 << 20
	spawnsPerRun = 40
	childArg     = "--speed-probe-child"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == childArg {
		return
	}
	when := flag.String("when", "", `"before" or "after" the BATS run`)
	dir := flag.String("dir", "", "directory for the disk test (default: the home directory)")
	flag.Parse()
	if *when != "before" && *when != "after" {
		fmt.Fprintln(os.Stderr, `--when must be "before" or "after"`)
		os.Exit(2)
	}
	// rdd keeps its instances, VM disks included, under the home directory.
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "home directory: %v\n", err)
			os.Exit(2)
		}
		*dir = home
	}

	record("probe.host", int64(runtime.NumCPU()), "cpus", *when, "model", cpuModel())

	hashBuf := make([]byte, hashSize)
	workloads := []struct {
		metric string
		run    func() error
	}{
		{"probe.cpu1", func() error { hash(hashBuf); return nil }},
		{"probe.cpuN", func() error { hashParallel(hashBuf); return nil }},
		{"probe.mem", copyMemory()},
		{"probe.spawn", spawn},
		{"probe.disk", func() error { return writeDisk(*dir) }},
	}
	failed := false
	for _, w := range workloads {
		ms, err := median(w.metric, w.run)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", w.metric, err)
			failed = true
			continue
		}
		record(w.metric, ms, "ms", *when)
	}
	if failed {
		os.Exit(1)
	}
}

// median runs f once per round and returns the median round time in
// milliseconds. It prints each round's time to stderr, so the job log
// shows how far the rounds spread.
func median(metric string, f func() error) (int64, error) {
	times := make([]time.Duration, rounds)
	for i := range times {
		start := time.Now()
		if err := f(); err != nil {
			return 0, err
		}
		times[i] = time.Since(start)
	}
	fmt.Fprintf(os.Stderr, "%s rounds: %v\n", metric, times)
	slices.Sort(times)
	return times[rounds/2].Milliseconds(), nil
}

// hash computes the SHA-256 of buf hashRepeats times.
func hash(buf []byte) {
	for range hashRepeats {
		sha256.Sum256(buf)
	}
}

// hashParallel runs hash on one goroutine per CPU and waits for all.
func hashParallel(buf []byte) {
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() { hash(buf) })
	}
	wg.Wait()
}

// copyMemory returns a workload that copies one buffer into another
// memRepeats times. Both buffers are larger than the CPU caches. They are
// filled first, so page faults stay out of the timed rounds.
func copyMemory() func() error {
	src := make([]byte, memSize)
	dst := make([]byte, memSize)
	for i := range src {
		src[i] = byte(i)
	}
	copy(dst, src)
	return func() error {
		for range memRepeats {
			copy(dst, src)
		}
		return nil
	}
}

// spawn starts the probe's own executable as a child that exits at once,
// so it measures process creation without needing any program from the OS.
func spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	for range spawnsPerRun {
		if out, err := exec.Command(exe, childArg).CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
	}
	return nil
}

// writeDisk writes diskSize bytes to a new file in dir, syncs it to disk,
// and removes it.
func writeDisk(dir string) (err error) {
	f, err := os.CreateTemp(dir, "speed-probe-*")
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, os.Remove(f.Name()))
	}()
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i)
	}
	for range diskSize / len(buf) {
		if _, err := f.Write(buf); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// cpuModel returns the CPU model name, or "unknown".
func cpuModel() string {
	var model string
	switch runtime.GOOS {
	case "linux":
		data, _ := os.ReadFile("/proc/cpuinfo")
		for line := range strings.Lines(string(data)) {
			if name, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "model name" {
				model = value
				break
			}
		}
	case "darwin":
		out, _ := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		model = string(out)
	case "windows":
		out, _ := exec.Command("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "/v", "ProcessorNameString").Output()
		// The value line reads "    ProcessorNameString    REG_SZ    <model>".
		for line := range strings.Lines(string(out)) {
			if _, value, ok := strings.Cut(line, "REG_SZ"); ok {
				model = value
				break
			}
		}
	}
	if model = strings.TrimSpace(model); model == "" {
		return "unknown"
	}
	return model
}

// record prints one result in the format of the "rdd-perf:" lines.
func record(metric string, value int64, unit, when string, labels ...string) {
	line := fmt.Sprintf("rdd-perf: v=1 suite=- file=- test=- ts=%d metric=%s value=%d unit=%s when=%s",
		time.Now().UnixMilli(), quote(metric), value, quote(unit), quote(when))
	for i := 0; i+1 < len(labels); i += 2 {
		line += fmt.Sprintf(" %s=%s", labels[i], quote(labels[i+1]))
	}
	fmt.Println(line)
}

// quote returns s as a quoted Go string when s is empty or contains a
// space, a quote, "=" or a character that strconv.Quote escapes, and
// returns s unchanged otherwise. It matches the quote function in
// rdd/pkg/perf, so a parser reads the probe's lines the same way as rdd's.
func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \"=") || strconv.Quote(s) != `"`+s+`"` {
		return strconv.Quote(s)
	}
	return s
}
