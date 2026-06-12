// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Probe-analyze tabulates the PROBE telemetry lines that the guest sampler
// (enabled via RDD_VM_TELEMETRY, see lima-template.yaml) writes to the
// serial console. It reads a serial log and emits one TSV row per sampler
// tick: wall time, load1, CPU split (user/system/iowait/idle percent,
// computed from /proc/stat deltas), PSI io avg10 when the kernel offers
// PSI, per-interval vda IOPS with average ms per I/O and io_ticks, the
// single-thread hash time (the vCPU-count-independent substrate speed),
// and the synced-write and O_DIRECT-read latency probes.
//
// Usage: go run scripts/probe-analyze.go <serial-log> [...]
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	tickRE     = regexp.MustCompile(`PROBE tick (\S+) load (\S+)`)
	psiRE      = regexp.MustCompile(`PROBE psi-(\w+) (.*)`)
	diskRE     = regexp.MustCompile(`PROBE diskstats\s+\d+\s+\d+\s+vda\s+(.*)`)
	cpustatRE  = regexp.MustCompile(`PROBE cpustat cpu\s+(.*)`)
	stHashRE   = regexp.MustCompile(`PROBE st-hash-ms (\d+)`)
	syncwRE    = regexp.MustCompile(`PROBE syncwrite-us (\d+)`)
	dreadRE    = regexp.MustCompile(`PROBE directread-us (\d+)`)
	psiAvg10RE = regexp.MustCompile(`avg10=([0-9.]+)`)
)

// row collects one sampler tick. Fields stay strings so absent probes
// render as empty TSV cells.
type row struct {
	t, load1, cpu, psiIO, iops, msPerIO, utilTicks, stHash, syncw, dread string
}

func ints(fields string) []int64 {
	parts := strings.Fields(fields)
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func analyze(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var rows []*row
	var cur *row
	var prevDisk, prevCPU []int64

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case tickRE.MatchString(line):
			m := tickRE.FindStringSubmatch(line)
			cur = &row{t: m[1], load1: strings.SplitN(m[2], "/", 2)[0]}
			rows = append(rows, cur)
		case cur == nil:
			// Ignore lines before the first tick.
		case psiRE.MatchString(line):
			m := psiRE.FindStringSubmatch(line)
			if m[1] != "io" {
				break
			}
			if strings.Contains(m[2], "absent") {
				cur.psiIO = "absent"
			} else if a := psiAvg10RE.FindStringSubmatch(m[2]); a != nil {
				cur.psiIO = a[1]
			}
		case diskRE.MatchString(line):
			// Fields: reads merged sectors read_ms writes merged sectors
			// write_ms inflight io_ticks weighted [...]
			f := ints(diskRE.FindStringSubmatch(line)[1])
			if prevDisk != nil && len(f) > 9 && len(prevDisk) > 9 {
				dio := (f[0] - prevDisk[0]) + (f[4] - prevDisk[4])
				dms := (f[3] - prevDisk[3]) + (f[7] - prevDisk[7])
				cur.iops = strconv.FormatInt(dio, 10)
				if dio > 0 {
					cur.msPerIO = fmt.Sprintf("%.1f", float64(dms)/float64(dio))
				}
				cur.utilTicks = strconv.FormatInt(f[9]-prevDisk[9], 10)
			}
			if len(f) > 9 {
				prevDisk = f
			}
		case cpustatRE.MatchString(line):
			// Fields: user nice system idle iowait irq softirq steal [...]
			f := ints(cpustatRE.FindStringSubmatch(line)[1])
			if prevCPU != nil && len(f) > 7 && len(prevCPU) > 7 {
				var total int64
				d := make([]int64, len(f))
				for i := range f {
					d[i] = f[i] - prevCPU[i]
					total += d[i]
				}
				if total > 0 {
					cur.cpu = fmt.Sprintf("%d/%d/%d/%d",
						d[0]*100/total, d[2]*100/total, d[4]*100/total, d[3]*100/total)
				}
			}
			if len(f) > 7 {
				prevCPU = f
			}
		case stHashRE.MatchString(line):
			cur.stHash = stHashRE.FindStringSubmatch(line)[1]
		case syncwRE.MatchString(line):
			us, _ := strconv.ParseInt(syncwRE.FindStringSubmatch(line)[1], 10, 64)
			cur.syncw = fmt.Sprintf("%.1f", float64(us)/1000)
		case dreadRE.MatchString(line):
			us, _ := strconv.ParseInt(dreadRE.FindStringSubmatch(line)[1], 10, 64)
			cur.dread = fmt.Sprintf("%.1f", float64(us)/1000)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	fmt.Fprintln(w, "t\tload1\tcpu%us/sy/io/id\tpsi_io\tiops\tms_per_io\tutil_ticks_ms\tst_hash_ms\tsyncw_ms\tdread_ms")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.t, r.load1, r.cpu, r.psiIO, r.iops, r.msPerIO, r.utilTicks, r.stHash, r.syncw, r.dread)
	}
	return w.Flush()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/probe-analyze.go <serial-log> [...]")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		if len(os.Args) > 2 {
			fmt.Fprintf(os.Stdout, "== %s\n", path)
		}
		if err := analyze(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
