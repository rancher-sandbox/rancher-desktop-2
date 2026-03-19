# Deep Review: 20260313-123900-rotate-serial-logs

| | |
|---|---|
| **Date** | 2026-03-13 12:39 |
| **Branch** | `rotate-serial-logs` |
| **Commits** | `4fd77c6` (serial log rotation)<br>`71714cf` (instance log preservation) |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Merge with fixes** — new functions lack unit tests, `svc delete` bypasses log preservation |

---

## 1. Consolidated Review

### Executive Summary

This branch adds two features: serial log rotation across VM restarts and instance log preservation on LimaVM deletion (when `RDD_KEEP_LOGS` is set). The `Rotate()` extraction from `Create()` is clean, well-tested, and handles edge cases correctly. The preservation logic is sound for its intended path (controller-managed deletion). **Merge with fixes** — the new `preserveInstanceLogs` and `nextAvailableDir` functions lack unit tests, and `svc delete` bypasses the preservation path entirely.

### Critical Findings

None confirmed. Codex and Gemini each raised a CRITICAL; both were investigated and downgraded (see below).

**[DOWNGRADED from CRITICAL] pkg/service/cmd/service.go:405 — Does `svc delete` orphan running VMs or destroy logs?**

Codex claimed that `svc delete` removes `ShortDir()` while Lima VMs may still run. Gemini claimed it destroys logs that `preserveInstanceLogs` was meant to keep.

Both claims rest on the assumption that `Stop()` does not wait for full shutdown. Investigation shows otherwise: `Stop()` calls `StopWithWait(true)`, which sends SIGTERM and polls for up to 60 seconds until the process exits. The control plane's graceful shutdown calls `shutdownAllHostagents()`, which sends SIGINT to every hostagent, waits up to 30 seconds per hostagent, and falls back to SIGKILL. By the time `Delete()` removes directories, all VMs are stopped.

The log-loss concern has more substance: `svc delete` does not run `preserveInstanceLogs`, so logs in `LimaHome` are wiped along with `ShortDir`. However, this is not a regression. Before this PR, BATS `local_teardown_file()` performed `rm -rf` on the same directories — logs were equally lost. The PR improves the common case (controller-managed deletion preserves logs) without worsening the `svc delete` case. The gap is addressed below as an IMPORTANT finding.

### Important Findings

**[IMPORTANT] limavm_lifecycle.go:550 — `os.Rename` fails across filesystems** [Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro]

Problem: `preserveInstanceLogs` uses `os.Rename` to move logs from `inst.Dir` (under `~/.rd*/lima/`) to `instance.LogDir()` (under `~/Library/Logs/`). On separate filesystems, `os.Rename` returns `EXDEV`. The error is logged and skipped, but `limainstance.Delete` then removes the source directory — losing the logs that `RDD_KEEP_LOGS` was supposed to preserve.

Fix: Add a copy-then-remove fallback when `os.Rename` returns `EXDEV`. Both paths normally share `$HOME`, so this is defensive rather than urgent.

**[IMPORTANT] limavm_lifecycle.go:516–578 — No unit tests for `preserveInstanceLogs` or `nextAvailableDir`** [Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro]

Problem: `nextAvailableDir` contains a loop with error-handling branches (ErrExist vs. other errors, exhaustion at 1000). `preserveInstanceLogs` has env-var gating, directory creation, file filtering, and rename-with-continue logic. Both are pure filesystem operations, straightforward to test in isolation. `Rotate()` received four new tests; these functions received none.

Fix: Add unit tests for `nextAvailableDir` (first call creates `{name}`, second creates `{name}.2`, non-ErrExist error propagates) and for `preserveInstanceLogs` (env-var gating, `.log` filtering, empty-directory avoidance).

**[IMPORTANT] limavm_lifecycle.go:56 + service.go:405 — `svc delete` bypasses log preservation** [Codex GPT 5.4, Gemini 3.1 Pro]

Problem: `preserveInstanceLogs` runs only during the controller's deletion reconcile. When `svc delete` stops the control plane and removes `ShortDir`, instance logs in `LimaHome` are wiped without preservation — even when `RDD_KEEP_LOGS` is set. In CI, if a test fails before the LimaVM resource is explicitly deleted, the next test's `setup_rdd_control_plane` calls `svc delete` and destroys the logs.

Fix: Before removing `ShortDir` in `Delete()`, iterate `LimaHome` subdirectories and move `.log` files to `LogDir()` when `RDD_KEEP_LOGS` is set. A shared helper could serve both the controller and the CLI.

### Suggestions

**[SUGGESTION] limavm_lifecycle.go:529–535 — Empty directory created when no log files exist** [Claude Opus 4.6, Gemini 3.1 Pro]

Problem: `preserveInstanceLogs` calls `nextAvailableDir` before checking whether any `.log` files exist. If the instance has none (e.g., VM created but never started), an empty directory remains in `LogDir`, consuming a numbered slot.

Fix: Read and filter entries first; call `nextAvailableDir` only when at least one `.log` file exists.

**[SUGGESTION] collect-bats-logs.sh:62–67 — Preserved-log collection picks up any subdirectory** [Claude Opus 4.6]

Problem: The glob `"$log_dir"/*/` matches every subdirectory of `log_dir`, not only directories created by `preserveInstanceLogs`. Harmless (only `.log` files are copied), but could create noise in CI artifacts.

Fix: Not urgent. If needed, prefix preservation directories (e.g., `instance-opensuse/`).

**[SUGGESTION] logfile.go:93–102 — Pruning advances cutoff when no file is rotated** [Gemini 3.1 Pro]

Problem: When `Rotate()` finds no active file to rename, it still computes `nextN = maxN + 1` and passes it to `pruneOldFiles`. This advances the cutoff window by one, potentially pruning the oldest retained file without adding a new one.

Fix: Skip pruning (or use `nextN = maxN`) when no file was renamed. In practice this is academic — serial logs that don't exist yet have no numbered backups either.

### Testing Assessment

**Well covered:**
- `Rotate()` has four new unit tests covering no-file no-op, single rename, sequential numbering, and pruning. These mirror the existing `Create()` test patterns.
- All existing `logfile` tests continue to pass.

**Gaps:**
- `preserveInstanceLogs` — no unit tests. BATS integration tests exercise the path (deletion with `RDD_KEEP_LOGS=1`), but no test verifies that preserved logs appear in `LogDir/{vm_name}/`.
- `nextAvailableDir` — no unit tests. Directory-numbering logic (first-call creates `{name}`, subsequent creates `{name}.2`, `{name}.3`) is untested.
- No test verifies that `rdd svc delete` removes `ShortDir()`.
- No test for the cross-filesystem rename failure path.
- `collect-bats-logs.sh` subdirectory collection is untested.

### Documentation Assessment

The documentation updates are thorough:
- `environment.md` gains a well-structured "Log Preservation" section covering all three behaviors, rotation mechanics, and BATS defaults.
- `cmd_service.md` now documents what `rdd service delete` does.
- The spelling dictionary update for `serialp`/`serialv` is correct.

One gap: `collect-bats-logs.sh` header comments still describe the old output layout and do not mention preserved-instance-log subdirectories.

---

## 2. Agent Performance Retro

### Claude Opus 4.6

- **Unique contributions:** Identified the `collect-bats-logs.sh` subdirectory-matching concern. Only agent to note that "docs are thorough, no gaps" (partially correct — Codex found the script header gap).
- **Accuracy:** High. No false positives. Correctly scoped the cross-filesystem issue as "almost never happens in practice."
- **Depth:** Did not investigate `Stop()` behavior; accepted the surface-level risk without verification. Explored file-level context well.
- **Signal-to-noise:** Excellent. Four findings, all actionable. No padding.

### Codex GPT 5.4

- **Unique contributions:** Raised the `svc delete` + running-VM concern (valid question, wrong conclusion). Only agent to explicitly flag that `preserveInstanceLogs` is best-effort and should return an error.
- **Accuracy:** The CRITICAL was incorrect — `Stop()` does wait for full shutdown. The "best-effort" observation is valid but debatable as a design choice.
- **Depth:** Referenced specific file/line numbers in context beyond the diff (`limavm_controller.go:468`). Good cross-file reasoning.
- **Signal-to-noise:** Moderate. Three findings, one false positive at CRITICAL severity.

### Gemini 3.1 Pro

- **Unique contributions:** Identified the pruning-advances-cutoff edge case in `Rotate()`. Raised the `svc delete` log-destruction scenario with a concrete CI narrative.
- **Accuracy:** The CRITICAL overstated the regression — it described a pre-existing gap, not a new bug. The pruning suggestion is technically correct but practically irrelevant.
- **Depth:** Good CI scenario reasoning. Did not verify `Stop()` behavior.
- **Signal-to-noise:** Moderate. Four findings, one overblown CRITICAL, one academic suggestion.

### Summary Table

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Findings | 4 | 3 | 4 |
| Unique insights | 1 | 1 | 2 |
| False positives | 0 | 1 (CRITICAL) | 1 (CRITICAL) |
| Depth (cross-file) | Medium | High | Medium |
| Signal-to-noise | High | Moderate | Moderate |

**Overall:** Claude Opus 4.6 provided the most reliable review — every finding was accurate and actionable. Codex and Gemini both contributed valuable unique insights (best-effort error handling, pruning edge case) but each misfired on a CRITICAL that investigation disproved. The combination of all three caught more than any single agent would have.

---

## 3. Skill Improvement Recommendations

### Prompt adjustments

- **Add "verify before flagging CRITICAL":** Both Codex and Gemini raised CRITICALs based on assumptions about `Stop()` behavior without reading the function. The prompt should instruct agents to verify control flow for CRITICAL findings by reading the relevant code paths, not just the diff.
- **Distinguish regressions from gaps:** All three agents struggled to separate "this PR introduces a bug" from "this PR doesn't close a pre-existing gap." The prompt should ask agents to classify each finding as regression, gap, or enhancement opportunity.

### Coverage gaps

- All three agents missed the script header comment staleness in `collect-bats-logs.sh` except Codex (partially).
- No agent examined whether `Rotate()` is safe under concurrent calls (two controllers racing to rotate the same serial log). The prompt's "race conditions" dimension should prompt agents to consider concurrent reconcile scenarios.

### Process improvements

- The clarification round (Step 5) was unnecessary here — all findings were clear. But the two false CRITICALs could have been caught by a targeted follow-up asking each agent to verify the `Stop()` → `StopWithWait` → poll-for-exit chain.

---

## 4. Appendix: Original Reviews

### Claude Opus 4.6

#### Executive Summary

This two-commit branch adds serial log rotation across VM restarts and preserves Lima instance logs on deletion when `RDD_KEEP_LOGS` is set. The `Rotate()` extraction from `Create()` is clean, well-tested, and correctly handles edge cases. The preservation logic is sound and the BATS teardown removal is safe now that `Delete()` cleans up `ShortDir()`. **Merge with minor fixes** — one correctness gap (cross-filesystem rename) deserves attention, and the new preservation functions lack unit test coverage.

#### Findings

**[IMPORTANT] limavm_lifecycle.go:550 — `os.Rename` fails across filesystems, silently losing logs**

Problem: `preserveInstanceLogs` uses `os.Rename` to move logs from `inst.Dir` (`~/.rd*/lima/<name>/`) to `instance.LogDir()` (`~/Library/Logs/rancher-desktop-*/`). If these paths are on different filesystems (NFS-backed home, split partitions, symlinks to different mounts), `os.Rename` returns `EXDEV`. The error is logged and skipped — but then `limainstance.Delete` removes `inst.Dir` including the un-moved log files. The user set `RDD_KEEP_LOGS` specifically to preserve these logs, so silently losing them defeats the purpose.

In practice this almost never happens (both paths live under `$HOME`), but the fix is straightforward.

Fix: Add a copy-and-delete fallback when `os.Rename` returns `*os.LinkError` with `syscall.EXDEV`.

**[IMPORTANT] limavm_lifecycle.go:516–578 — No unit tests for `preserveInstanceLogs` or `nextAvailableDir`**

Problem: `nextAvailableDir` has a loop with error-handling branches (ErrExist vs other errors, exhaustion at 1000). `preserveInstanceLogs` has env-var gating, directory creation, file filtering, and rename-with-continue logic. Both are pure filesystem operations that are straightforward to test in isolation. The `Rotate()` function got four new tests; these functions got none.

Fix: Add unit tests for `nextAvailableDir` at minimum. Test cases: first call creates `{name}`, second creates `{name}.2`, non-ErrExist error propagates, and the `.log` file filtering in `preserveInstanceLogs` (dirs and non-.log files are skipped).

**[SUGGESTION] limavm_lifecycle.go:531–535 — Directory created before checking for log files**

Problem: `nextAvailableDir` creates the destination directory unconditionally. If the instance has no `.log` files (e.g., VM created but never started), an empty directory is left behind, consuming a numbered slot and producing a `"count": 0` log line.

Fix: Read and filter entries first, skip preservation entirely if no `.log` files exist.

**[SUGGESTION] collect-bats-logs.sh:62–67 — Preserved log collection picks up any subdirectory**

Problem: The glob `"$log_dir"/*/` matches ALL subdirectories of `log_dir`, not just those created by `preserveInstanceLogs`. Harmless (only `.log` files are copied), but could create noise in CI artifacts.

Fix: Not urgent. If it ever matters, the preservation directories could use a prefix.

#### Testing Assessment

Well covered: `Rotate()` has four new unit tests. Not covered: `preserveInstanceLogs`, `nextAvailableDir`, cross-filesystem rename failure, `svc delete` removing `ShortDir()`.

#### Documentation Assessment

Thorough. No gaps identified.

---

### Codex GPT 5.4

#### Executive Summary

This branch adds two useful behaviors: serial log rotation before Lima restarts, and optional preservation of Lima instance logs when a VM is deleted with `RDD_KEEP_LOGS` set. It should not merge as-is: one change makes `rdd svc delete` unsafe around running Lima VMs, and the new log-preservation path can still silently lose the logs it promises to keep.

#### Findings

**[CRITICAL] pkg/service/cmd/service.go:401 — `svc delete` now removes Lima state without stopping Lima VMs**

Problem: `Delete()` only stops the RDD control plane, then immediately `RemoveAll(instance.ShortDir())`. The controller shutdown path only terminates hostagents, not the VM driver itself. If a Lima VM is still running, `svc delete` can now delete `LIMA_HOME` out from under a live QEMU/VZ instance, orphaning the VM process and corrupting its on-disk state.

Fix: Before removing ShortDir, enumerate Lima instances under `instance.LimaHome()` and force-stop/delete them first, or refuse to delete `ShortDir()` while any Lima instance is still running.

**[IMPORTANT] limavm_lifecycle.go:56 — log preservation is best-effort, so `RDD_KEEP_LOGS` can still lose logs**

Problem: `handleDeletion()` always proceeds to `limainstance.Delete()` after calling `preserveInstanceLogs()`, but `preserveInstanceLogs()` only logs errors and returns on failures. In those cases the subsequent Lima delete wipes the instance directory anyway, so the logs are lost despite `RDD_KEEP_LOGS` being explicitly enabled for preservation.

Fix: Make `preserveInstanceLogs()` return an error and abort/retry VM deletion when preservation fails.

**[IMPORTANT] limavm_lifecycle.go:519 — the second commit's filesystem behavior is effectively untested**

Problem: The only new automated coverage is for `logfile.Rotate()`. There is no unit or BATS coverage for `preserveInstanceLogs()`, `nextAvailableDir()`, `svc delete` removing ShortDir, or `scripts/collect-bats-logs.sh` picking up preserved subdirectories.

Fix: Add unit tests for `preserveInstanceLogs()` and `nextAvailableDir()`, plus a BATS flow verifying the full preservation lifecycle.

#### Testing Assessment

`go test` passes, but the controller package has no tests, so only `Rotate()` is exercised. Untested: `svc delete` while a VM runs, repeated deletions, partial preservation failures, collection script, end-to-end serial log preservation.

#### Documentation Assessment

Directionally correct. Gap: `collect-bats-logs.sh` header comments do not mention preserved instance-log subdirectories.

---

### Gemini 3.1 Pro

#### Executive Summary

This PR introduces log rotation for serial logs and implements instance log preservation during controller-managed VM deletion when `RDD_KEEP_LOGS` is set. However, it requires rework before merging due to a critical oversight where `rdd svc delete` aggressively wipes the instance directory without preserving logs.

#### Findings

**[CRITICAL] pkg/service/cmd/service.go:405 — `rdd svc delete` destroys the logs it intends to preserve**

Problem: `ShortDir()` contains `instance.LimaHome()`, where all active VM logs reside. `svc delete` executes a recursive removal that bypasses the `LimaVMReconciler`'s `preserveInstanceLogs` logic. In CI, if a test fails and leaves a running VM, the next test's `setup_rdd_control_plane` runs `rdd svc delete`, permanently destroying the logs before `collect-bats-logs.sh` can retrieve them.

Fix: If `RDD_KEEP_LOGS` is set, `Delete()` must iterate over `instance.LimaHome()` subdirectories and move their `.log` files to `instance.LogDir()` before wiping `ShortDir()`.

**[IMPORTANT] limavm_lifecycle.go:529–533 — Creates empty directories when no logs exist**

Problem: `preserveInstanceLogs` calls `nextAvailableDir` before checking whether `.log` files exist. If an instance has no logs, an empty directory remains in `LogDir()`. In a crash-loop scenario, this can clutter the log directory.

Fix: Delay `destDir` creation until the first `.log` file match.

**[SUGGESTION] logfile.go:94–96 — Pruning logic off-by-one when active log is missing**

Problem: In `Rotate`, if the active log file does not exist, `Rotate` still falls through to call `pruneOldFiles` with `nextN` equal to `maxN + 1`. This artificially advances the cutoff window, causing the oldest retained file to be deleted without a new file taking its place.

Fix: Only advance `nextN` and execute `pruneOldFiles` if a file was successfully rotated.

**[SUGGESTION] limavm_lifecycle.go:547 — `os.Rename` fails across devices**

Problem: `preserveInstanceLogs` uses `os.Rename` which fails with `EXDEV` on cross-device moves.

Fix: Implement a copy-then-delete fallback for `EXDEV`.

#### Testing Assessment

Excellent coverage for `Rotate()`. No unit or integration tests for `preserveInstanceLogs`. An explicit integration test verifying the log preservation lifecycle across both `limaVM` deletion and `svc delete` is recommended.

#### Documentation Assessment

Documentation correctly outlines rotation and preservation. Gap: `cmd_service.md` and `environment.md` do not mention that active instance logs within `ShortDir` are wiped by `svc delete` unless preservation runs first.
