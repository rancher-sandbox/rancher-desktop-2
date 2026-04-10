# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Sample CPU, disk, and memory counters once per second, plus the top 10
# processes by working set. Writes one JSON object per line to $OutputPath.
# The resulting JSONL can be correlated against rdd.stderr timestamps to
# identify what contended for CPU/disk during slow intervals.
#
# Called by the windows-perf-sampler composite action; not intended for
# direct invocation.

param(
    [Parameter(Mandatory)][string]$OutputPath,
    [int]$IntervalSeconds = 1
)

$ErrorActionPreference = 'Continue'
$ProgressPreference   = 'SilentlyContinue'

$dir = Split-Path -Parent $OutputPath
if ($dir -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

# Truncate any previous run.
Set-Content -Path $OutputPath -Value '' -NoNewline

# Stable English counter paths. These are the same paths Get-Counter accepts
# on US-locale runners (which is what GitHub Actions windows-latest is).
$counters = @(
    '\Processor(_Total)\% Processor Time'
    '\System\Processor Queue Length'
    '\Memory\Available MBytes'
    '\Memory\% Committed Bytes In Use'
    '\PhysicalDisk(_Total)\% Disk Time'
    '\PhysicalDisk(_Total)\Disk Read Bytes/sec'
    '\PhysicalDisk(_Total)\Disk Write Bytes/sec'
    '\PhysicalDisk(_Total)\Avg. Disk sec/Read'
    '\PhysicalDisk(_Total)\Avg. Disk sec/Write'
    '\PhysicalDisk(_Total)\Current Disk Queue Length'
)

# Track previous CPU values per PID so we can emit per-second deltas
# (Get-Process .CPU is cumulative TotalProcessorTime in seconds).
$prevCpu = @{}

while ($true) {
    $iterStart = Get-Date

    $event = [ordered]@{
        time = $iterStart.ToUniversalTime().ToString('o')
    }

    # System counters.
    try {
        $sample = Get-Counter -Counter $counters -ErrorAction SilentlyContinue
        if ($sample) {
            $sys = [ordered]@{}
            foreach ($s in $sample.CounterSamples) {
                # Strip leading "\\hostname" so paths are stable across runners.
                $key = $s.Path -replace '^\\\\[^\\]+', ''
                $sys[$key] = [math]::Round($s.CookedValue, 4)
            }
            $event['sys'] = $sys
        }
    } catch {
        $event['sys_error'] = $_.Exception.Message
    }

    # Per-process snapshot: top 10 by working set, plus per-second CPU delta.
    try {
        $allProcs = Get-Process -ErrorAction SilentlyContinue
        $newPrev  = @{}
        $procRows = foreach ($p in $allProcs) {
            $cpuNow = if ($null -ne $p.CPU) { $p.CPU } else { 0 }
            $cpuPrev = if ($prevCpu.ContainsKey($p.Id)) { $prevCpu[$p.Id] } else { $cpuNow }
            $delta   = $cpuNow - $cpuPrev
            $newPrev[$p.Id] = $cpuNow
            [PSCustomObject]@{
                name      = $p.ProcessName
                id        = $p.Id
                ws_mb     = [math]::Round($p.WorkingSet64 / 1MB, 1)
                cpu_delta = [math]::Round($delta, 3)
                handles   = $p.HandleCount
                threads   = $p.Threads.Count
            }
        }
        $prevCpu = $newPrev

        # Top by working set is generally more informative for "what's
        # consuming RAM" while top by cpu_delta is what we actually need
        # to find a runaway process. Take the union.
        $byWs    = $procRows | Sort-Object -Property ws_mb     -Descending | Select-Object -First 10
        $byCpu   = $procRows | Sort-Object -Property cpu_delta -Descending | Select-Object -First 10
        $event['top_by_ws']  = $byWs
        $event['top_by_cpu'] = $byCpu
    } catch {
        $event['proc_error'] = $_.Exception.Message
    }

    # Append one compact JSON object per line.
    try {
        ($event | ConvertTo-Json -Compress -Depth 5) + "`n" |
            Add-Content -Path $OutputPath -NoNewline
    } catch {
        # If the output path becomes unwritable, log to stderr and keep going.
        [Console]::Error.WriteLine("perf-sampler: write failed: $($_.Exception.Message)")
    }

    # Sleep the remainder of the interval.
    $elapsedMs = ((Get-Date) - $iterStart).TotalMilliseconds
    $sleepMs   = ($IntervalSeconds * 1000) - $elapsedMs
    if ($sleepMs -gt 0) {
        Start-Sleep -Milliseconds ([int]$sleepMs)
    }
}
