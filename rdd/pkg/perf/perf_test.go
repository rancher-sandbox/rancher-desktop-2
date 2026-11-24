// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package perf

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestFormatLine(t *testing.T) {
	now := time.UnixMilli(1763921829453)
	line := formatLine(now, "limavm.boot.req", 5123, UnitMS,
		[]string{"vm", "rd", "req", "Boot scripts to be finished", "q", `a"b`, "eq", "a=b", "empty", "", "dangling"})
	assert.Equal(t, line, `1763921829453 metric=limavm.boot.req value=5123 unit=ms vm=rd req="Boot scripts to be finished" q="a\"b" eq="a=b" empty="" dangling=""`+"\n")
}

func TestRecordAppendsOneLinePerRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.log")
	t.Setenv(LogEnv, path)

	const writers, records = 8, 50
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range records {
				Record(t.Context(), "cp.total", 42, UnitMS, "req", "a long label value")
			}
		})
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	assert.Equal(t, len(lines), writers*records)
	for _, line := range lines {
		_, rest, ok := strings.Cut(line, " ")
		assert.Assert(t, ok, line)
		assert.Equal(t, rest, `metric=cp.total value=42 unit=ms req="a long label value"`)
	}
}

func TestClockLap(t *testing.T) {
	start := time.Now()
	c := &Clock{last: start}
	assert.Equal(t, c.lap(start.Add(300*time.Millisecond)), 300*time.Millisecond)
	assert.Equal(t, c.lap(start.Add(1000*time.Millisecond)), 700*time.Millisecond)
}

func TestNilClockAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf.log")
	t.Setenv(LogEnv, path)

	ClockFrom(t.Context()).Mark(t.Context(), "cp.ready")
	_, err := os.Stat(path)
	assert.Assert(t, os.IsNotExist(err))

	c := NewClock()
	assert.Equal(t, ClockFrom(WithClock(t.Context(), c)), c)
}
