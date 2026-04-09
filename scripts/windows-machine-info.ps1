# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# One-shot Windows machine info dump for the BATS slowdown investigation.
# Captures hardware, OS, disk, memory, Defender, and WSL state so we can
# correlate slow gzip decompressions on the runner with environmental factors
# the rdd.stderr logs do not record.
#
# Usage: pwsh -File scripts/windows-machine-info.ps1 -OutputPath <file>
#
# This is a temporary diagnostic. Output is plain text intended for human
# reading from a CI artifact.

param(
    [Parameter(Mandatory)][string]$OutputPath
)

$ErrorActionPreference = 'Continue'
$dir = Split-Path -Parent $OutputPath
if ($dir -and -not (Test-Path $dir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

# Tee everything written by Write-Output to both the console and $OutputPath.
Start-Transcript -Path $OutputPath -Force | Out-Null

try {
    Write-Output "=== Date ==="
    (Get-Date).ToUniversalTime().ToString('o')
    Write-Output ''

    Write-Output "=== ComputerInfo ==="
    Get-ComputerInfo |
        Select-Object CsName, CsManufacturer, CsModel, CsNumberOfProcessors,
                      CsNumberOfLogicalProcessors, CsTotalPhysicalMemory,
                      OsName, OsVersion, OsBuildNumber, OsArchitecture,
                      OsLastBootUpTime, OsSystemDrive |
        Format-List

    Write-Output "=== CPUs ==="
    Get-CimInstance Win32_Processor |
        Select-Object Name, NumberOfCores, NumberOfLogicalProcessors,
                      MaxClockSpeed, CurrentClockSpeed,
                      L2CacheSize, L3CacheSize, ProcessorId |
        Format-List

    Write-Output "=== Memory ==="
    $os = Get-CimInstance Win32_OperatingSystem
    [PSCustomObject]@{
        TotalVisibleMemoryMB = [math]::Round($os.TotalVisibleMemorySize / 1024, 1)
        FreePhysicalMemoryMB = [math]::Round($os.FreePhysicalMemory / 1024, 1)
        TotalVirtualMemoryMB = [math]::Round($os.TotalVirtualMemorySize / 1024, 1)
        FreeVirtualMemoryMB  = [math]::Round($os.FreeVirtualMemory / 1024, 1)
        FreeSpaceInPagingFilesMB = [math]::Round($os.FreeSpaceInPagingFiles / 1024, 1)
    } | Format-List

    Write-Output "=== Physical Disks ==="
    Get-PhysicalDisk |
        Select-Object DeviceID, FriendlyName, MediaType, BusType, Model,
                      @{N='SizeGB';E={[math]::Round($_.Size/1GB,1)}},
                      @{N='AllocatedGB';E={[math]::Round($_.AllocatedSize/1GB,1)}},
                      HealthStatus, OperationalStatus, SpindleSpeed |
        Format-Table -AutoSize

    Write-Output "=== Logical Disks ==="
    Get-CimInstance Win32_LogicalDisk |
        Select-Object DeviceID, DriveType, FileSystem,
                      @{N='SizeGB';E={[math]::Round($_.Size/1GB,1)}},
                      @{N='FreeGB';E={[math]::Round($_.FreeSpace/1GB,1)}},
                      VolumeName |
        Format-Table -AutoSize

    Write-Output "=== Disk Performance Counters (instantaneous) ==="
    Get-Counter -Counter @(
        '\PhysicalDisk(_Total)\Avg. Disk sec/Write',
        '\PhysicalDisk(_Total)\Avg. Disk sec/Read',
        '\PhysicalDisk(_Total)\Disk Write Bytes/sec',
        '\PhysicalDisk(_Total)\Disk Read Bytes/sec',
        '\PhysicalDisk(_Total)\Current Disk Queue Length'
    ) -ErrorAction SilentlyContinue | Format-List

    Write-Output "=== Disk Throughput Benchmark ==="
    # Sequential write/read of a 256 MB file in TEMP. This measures the disk
    # path the bats run actually uses (Lima's instance dirs and the lima
    # download cache live under %USERPROFILE% / %LOCALAPPDATA%, on the same
    # volume as %TEMP% on a default GitHub runner). FileStream with no
    # buffering still goes through the OS file cache, so this is the
    # cache-warm sequential rate, not raw disk speed -- but that is what
    # gzip writes hit too.
    $benchFile = Join-Path $env:TEMP "diag-disk-bench-$([System.Guid]::NewGuid().ToString('N')).bin"
    try {
        $sizeMB = 256
        $buf = New-Object byte[] (1MB)
        (New-Object Random).NextBytes($buf)

        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        $fs = [System.IO.File]::Create($benchFile)
        for ($i = 0; $i -lt $sizeMB; $i++) {
            $fs.Write($buf, 0, $buf.Length)
        }
        $fs.Flush($true)  # flush to disk
        $fs.Close()
        $sw.Stop()
        $writeMs = $sw.Elapsed.TotalMilliseconds
        $writeMBs = [math]::Round($sizeMB * 1000 / $writeMs, 1)

        $sw.Restart()
        $fs = [System.IO.File]::OpenRead($benchFile)
        $total = 0
        $readbuf = New-Object byte[] (1MB)
        while (($n = $fs.Read($readbuf, 0, $readbuf.Length)) -gt 0) {
            $total += $n
        }
        $fs.Close()
        $sw.Stop()
        $readMs = $sw.Elapsed.TotalMilliseconds
        $readMBs = [math]::Round($sizeMB * 1000 / $readMs, 1)

        [PSCustomObject]@{
            File = $benchFile
            SizeMB = $sizeMB
            WriteMs = [math]::Round($writeMs, 1)
            WriteMBPerSec = $writeMBs
            ReadMs  = [math]::Round($readMs, 1)
            ReadMBPerSec  = $readMBs
        } | Format-List
    } catch {
        Write-Output "disk bench failed: $($_.Exception.Message)"
    } finally {
        if (Test-Path $benchFile) { Remove-Item $benchFile -Force -ErrorAction SilentlyContinue }
    }

    Write-Output "=== Defender Status ==="
    Get-MpComputerStatus -ErrorAction SilentlyContinue |
        Select-Object AntivirusEnabled, RealTimeProtectionEnabled,
                      OnAccessProtectionEnabled, BehaviorMonitorEnabled,
                      IoavProtectionEnabled, NISEnabled, IsTamperProtected,
                      AntivirusSignatureLastUpdated, QuickScanStartTime,
                      FullScanStartTime |
        Format-List

    Write-Output "=== Defender Preferences (subset) ==="
    Get-MpPreference -ErrorAction SilentlyContinue |
        Select-Object DisableRealtimeMonitoring, DisableBehaviorMonitoring,
                      DisableScanningNetworkFiles, DisableArchiveScanning,
                      ScanAvgCPULoadFactor, ExclusionPath, ExclusionProcess |
        Format-List

    Write-Output "=== WSL ==="
    & wsl.exe --status
    Write-Output ''
    & wsl.exe --list --verbose
    Write-Output ''
    & wsl.exe --version 2>&1
    Write-Output ''
    Write-Output "--- .wslconfig (if present) ---"
    $wslconf = Join-Path $env:USERPROFILE '.wslconfig'
    if (Test-Path $wslconf) { Get-Content $wslconf } else { "(no .wslconfig)" }

    Write-Output ''
    Write-Output "=== Hyper-V services ==="
    Get-Service vmms, vmcompute, hns, WslService -ErrorAction SilentlyContinue |
        Select-Object Name, Status, StartType | Format-Table -AutoSize

    Write-Output "=== Top 20 processes by working set ==="
    Get-Process | Sort-Object -Property WS -Descending | Select-Object -First 20 |
        Select-Object @{N='WS_MB';E={[math]::Round($_.WorkingSet64/1MB,1)}},
                      @{N='CPU_sec';E={if ($_.CPU) { [math]::Round($_.CPU,1) } else { '' }}},
                      Id, Handles, ProcessName |
        Format-Table -AutoSize

    Write-Output "=== Environment ==="
    Get-ChildItem env: | Where-Object { $_.Name -match '^(LOCALAPPDATA|TEMP|TMP|USERPROFILE|HOMEDRIVE|GITHUB_|RUNNER_|RDD_|MSYS|PATH)$' } |
        Sort-Object Name | Format-Table -AutoSize Name, Value
}
finally {
    Stop-Transcript | Out-Null
}
