# Deep Review: app-controller-create-delete-limavm-20260313-180000

| | |
|---|---|
| **Date** | 2026-03-13 18:00 |
| **Branch** | `app-controller-create-delete-limavm` |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Merge with fixes** — controller cannot progress past ConfigMap creation, status mirroring has design issues |

---

## Consolidated Review

### Executive Summary

This branch implements App controller lifecycle management for LimaVM (create,
delete, status mirroring), adds MSYS2 BATS test support, and bumps yq. The
controller design is sound — the deletion flow correctly preserves the LimaVM
finalizer for clean VM teardown, and the cross-scope ownership works. One
critical bug prevents the controller from progressing past ConfigMap creation,
and the status mirroring has two design issues. **Merge with fixes.**

**Structure:** 5 commits — App controller lifecycle (1), MSYS2 BATS support
(2), yq dependency bump (2).

### Critical Issues

1. **Controller stalls after creating input ConfigMap** — `app_controller.go:137` [Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro]

```go
if err := r.Create(ctx, inputCM); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
}
return ctrl.Result{}, nil // requeue; LimaVM creation happens next iteration
```

All three agents identified this independently. `ctrl.Result{}, nil` tells
controller-runtime "reconciliation is done; wait for the next watch event."
`SetupWithManager` watches `For(&v1alpha1.App{})` and
`Owns(&limav1alpha1.LimaVM{})` but does not watch ConfigMaps. Creating a
ConfigMap owned by the App triggers no watch event. The controller never
reconciles again, and the LimaVM is never created.

This differs from line 105 (where `EnsureFinalizer` updates the App, triggering
the `For` watch) and line 162 (where creating a LimaVM triggers the `Owns`
watch). On line 137, neither the App nor a LimaVM changes.

Fix: return `ctrl.Result{Requeue: true}, nil`, or create both the ConfigMap and
the LimaVM in the same reconcile (the webhook reads ConfigMaps through
`mgr.GetAPIReader()`, not the informer cache, so it does not need a cache-warm
delay).

---

### Important Issues

1. **Stale App conditions survive LimaVM recreation** — `app_controller.go:186-203` [Codex GPT 5.4]

```go
statusChanged := false
for _, cond := range limaVM.Status.Conditions {
    statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
        Type:   cond.Type,
        Status: cond.Status,
        ...
    }) || statusChanged
}
```

The mirror loop upserts conditions from the LimaVM but never removes conditions
the LimaVM no longer publishes. A newly created LimaVM starts with only
`Created=Unknown`. If the App previously mirrored `Running=True` from an older
LimaVM, that stale condition persists until the new LimaVM eventually publishes
its own `Running` condition.

Fix: rebuild the mirrored set from scratch each reconcile. Clear App conditions
sourced from LimaVM, then repopulate from `limaVM.Status.Conditions`. Preserve
any App-native conditions if those are added later.

2. **ObservedGeneration copied from wrong resource** — `app_controller.go:194` [Claude Opus 4.6, Gemini 3.1 Pro]

```go
ObservedGeneration: cond.ObservedGeneration,
```

`ObservedGeneration` in a LimaVM condition refers to `.metadata.generation` of
the LimaVM. Copying it onto the App condition violates the Kubernetes convention
that `ObservedGeneration` reflects the generation of the resource that owns the
condition. The LimaVM controller currently sets this to 0, so practical impact
is nil today, but it will mislead clients if the LimaVM controller starts
setting it correctly.

Fix: use `app.Generation`.

3. **`r.Update` for spec propagation risks 409 conflicts** — `app_controller.go:180` [Claude Opus 4.6]

```go
limaVM.Spec.Running = app.Spec.Running
if err := r.Update(ctx, limaVM); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to propagate running state to LimaVM: %w", err)
}
```

`Update` sends the full object and fails with 409 Conflict if the LimaVM's
`resourceVersion` changed since the `Get` at line 112. The LimaVM controller
concurrently updates status, which can bump the resource version. Returning the
409 triggers a requeue with backoff, so this self-heals, but it adds
unnecessary latency to a time-sensitive operation (starting/stopping the VM).

Fix: use a merge patch:
```go
patch := client.MergeFrom(limaVM.DeepCopy())
limaVM.Spec.Running = app.Spec.Running
if err := r.Patch(ctx, limaVM, patch); err != nil { ... }
```

4. **No automated test coverage** — `app_controller.go` [Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro]

This change adds a multi-reconcile create path, finalizer-driven cross-controller
delete path, and status mirroring, but ships no unit or BATS tests. The Demo
controller (a comparable singleton) has a 295-line BATS suite at
`bats/tests/32-app-controllers/demo.bats`. The App controller should follow the
same pattern. This gap is what allowed the stall bug to reach review.

---

### Suggestions

1. **Comment typo: "into to"** — `app_controller.go:177` [Claude Opus 4.6]

```go
// Propagate spec.running from App into to the LimaVM.
```

Fix: "into the LimaVM" (remove "to").

2. **Grammar in comment: "registers Lima types allows"** — `controller.go:63` [Claude Opus 4.6]

```go
// It also registers Lima types allows App controller to create and watch LimaVM resources.
```

Fix: "It registers Lima types so the App controller can create and watch LimaVM
resources."

3. **lima.yaml provides only an amd64 image** — `lima.yaml:3` [Claude Opus 4.6]

```yaml
images:
- location: https://...distro.v0.1.0.amd64.qcow2
```

No arm64 image. On Apple Silicon with `vmType: qemu`, Lima will fail or fall
back to emulation. Acceptable if the template is temporary (per the comment in
`controller.go:18`), but worth noting.

---

### Testing Assessment

All three agents agree: the change ships no tests despite implementing complex
multi-reconcile state machine logic. Untested scenarios ranked by risk:

1. **Happy path: App creation triggers LimaVM creation** — would have caught
   the critical stall bug.
2. **App deletion waits for LimaVM teardown** — multiple deletion branches
   (LimaVM gone, LimaVM not yet deleting, LimaVM deleting) each need coverage.
3. **`spec.running` propagation from App to LimaVM**.
4. **Status condition mirroring** — including LimaVM recreation (stale
   condition bug) and intermediate states where LimaVM publishes only a subset
   of conditions.
5. **Re-creation after deletion** — deleting the App and creating a new one
   should produce a fresh LimaVM with no stale conditions.

### Documentation Assessment

- `docs/development.md` MSYS2 section: clear and appropriate.
- `app_types.go` immutability comment on `Namespace`: useful.
- Inline comments in the reconcile loop explain the "why" well, particularly
  the deletion comment block (lines 54-67).
- The `lima.yaml` template is marked temporary (`controller.go:18`) but has no
  tracking issue or TODO for its removal.
- No design documentation explains the App-to-LimaVM lifecycle contract or the
  deliberate avoidance of `base.DeleteOwnedResources`.

---

## Agent Performance Retro

### Claude Opus 4.6

- **Unique contributions**: 409 conflict risk with `Update` vs `Patch` (traced
  into LimaVM controller to verify concurrent status updates), amd64-only image
  note, comment typos.
- **Accuracy**: No false positives. All findings verified via git blame and code
  tracing.
- **Depth**: Strongest contextual analysis. Traced into the LimaVM controller's
  status update path to verify the 409 conflict risk. Correctly distinguished
  line 137 (no event trigger) from lines 105 and 162 (both trigger events).
- **Signal-to-noise**: High. Every finding is actionable.

### Codex GPT 5.4

- **Unique contributions**: Stale conditions surviving LimaVM recreation — the
  most important finding after the stall bug. No other agent caught this.
- **Accuracy**: No false positives.
- **Depth**: Good. Traced the LimaVM create path to verify that a new LimaVM
  starts with only `Created=Unknown`, leaving stale `Running=True` on the App.
- **Signal-to-noise**: High. Focused output with no filler.

### Gemini 3.1 Pro

- **Unique contributions**: None that were both unique and correctly calibrated.
- **Accuracy**: One false positive at the critical level. Gemini's Critical #2
  (transient error during deletion leads to "orphaned forever" ConfigMap) is
  overstated — the ConfigMap has an ownerReference to the App, so Kubernetes GC
  deletes it when the App is removed. The "unnecessary polling" finding
  (Important #2) is debatable: `RequeueAfter` during deletion is a standard
  defensive pattern against missed watch events.
- **Depth**: Adequate for the App controller but did not trace into the LimaVM
  controller or verify claims about GC behavior.
- **Signal-to-noise**: Lower than the other agents due to severity inflation.

### Summary Table

Agents run concurrently and compete for system resources; durations are
approximate.

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Duration (s) | 585 | 369 | 326 |
| Critical | 1 | 1 | 2 (1 overstated) |
| Important | 3 | 2 | 3 |
| Suggestion | 2 | 0 | 0 |
| False positives | 0 | 0 | 1 |
| Unique insights | 2 | 1 | 0 |

**Overall assessment**: Claude provided the most thorough analysis with the best
contextual tracing. Codex delivered the highest-value unique insight (stale
conditions) with the most concise output. Gemini was the fastest but inflated
severity on one finding and missed the stale conditions and conflict risk issues
that the other agents caught.

---

## Skill Improvement Recommendations

- **Review dimensions were adequate.** The prompt correctly directed agents to
  trace cross-controller interactions, which produced the highest-value findings.
- **Severity calibration guidance helped.** The embedded-kube-apiserver context
  prevented multi-tenant false positives that appeared in the first deep review.
- **Suggested prompt adjustment**: add explicit instruction to check whether
  `ctrl.Result{}, nil` returns are reachable by a watch event. This is a common
  controller-runtime pitfall and all three agents found it, but only because
  this instance was obvious. Subtler cases (e.g., an indirect event chain)
  might be missed.
- **Aspects all agents missed**: None identified — the three agents collectively
  covered all significant issues.
- **Process improvement**: The stale-condition finding (Codex) and the 409
  conflict finding (Claude) were each caught by only one agent. This validates
  the multi-agent approach: a single-agent review would have missed one or both.

---

## Appendix: Original Reviews

### Claude Opus 4.6

---

## Executive Summary

This branch adds App controller lifecycle management for LimaVM resources, a yq dependency bump, and MSYS2 BATS test support (already merged to main). The App controller change is well-structured: the deletion flow correctly preserves the LimaVM finalizer for clean teardown, the cross-scope ownership works, and the webhook interaction is race-free. However, one critical bug prevents the controller from progressing past ConfigMap creation, and the change ships with no tests. **Merge with fixes.**

**Structure:** 3 unmerged commits (1 substantive App controller change, 2 yq bump). The MSYS2 changes listed in the diff are already on main.

---

## Findings

### Critical Issues

1. **Controller stalls after creating input ConfigMap** — `app_controller.go:137` (critical, regression)

```go
if err := r.Create(ctx, inputCM); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
}
return ctrl.Result{}, nil // requeue; LimaVM creation happens next iteration
```

`ctrl.Result{}, nil` means "done, wait for the next watch event." The controller watches `For(&v1alpha1.App{})` and `Owns(&limav1alpha1.LimaVM{})` (line 212), but does NOT watch ConfigMaps. Creating a ConfigMap owned by the App triggers no watch event. No explicit requeue is requested. The controller never reconciles again, and the LimaVM is never created.

This differs from line 105 (`EnsureFinalizer` updates the App, triggering a `For` watch event) and line 162 (creating a LimaVM triggers the `Owns` watch). On line 137, nothing changes the App and no LimaVM exists.

**Verified via git blame:** line 137 introduced in commit `7545302`.

Fix: either `return ctrl.Result{Requeue: true}, nil` to trigger immediate re-reconciliation, or remove the split and create both ConfigMap and LimaVM in the same reconcile (the webhook uses `mgr.GetAPIReader()`, so it reads the ConfigMap directly from kine, not the informer cache).

### Important Issues

1. **ObservedGeneration copied from wrong resource** — `app_controller.go:194` (important, regression)

```go
statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
    ...
    ObservedGeneration: cond.ObservedGeneration,
    ...
}) || statusChanged
```

The `ObservedGeneration` in a LimaVM condition refers to `.metadata.generation` of the LimaVM. Copying it verbatim onto the App condition violates the Kubernetes convention that `ObservedGeneration` should reflect the generation of the resource that owns the condition. Currently the LimaVM controller sets it to 0, so the practical impact is nil. But if LimaVM starts setting it correctly, App conditions will carry a misleading generation number.

Fix: use `app.Generation` instead:
```go
ObservedGeneration: app.Generation,
```

2. **`r.Update(ctx, limaVM)` for spec propagation risks conflict errors** — `app_controller.go:180` (important, regression)

```go
limaVM.Spec.Running = app.Spec.Running
if err := r.Update(ctx, limaVM); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to propagate running state to LimaVM: %w", err)
}
```

`Update` sends the full object and fails with a 409 Conflict if the LimaVM's `resourceVersion` changed since the `Get` at line 112 (for example, the LimaVM controller updating status concurrently). The LimaVM controller avoids this by using `client.MergeFrom(limaVM.DeepCopy())` patches for its status updates (`limavm_controller.go:440-446`).

Returning the 409 as an error does trigger a requeue with backoff, so this is self-healing — but it adds unnecessary latency to a time-sensitive operation (starting/stopping the VM). Patching only the `spec.running` field eliminates the conflict.

Fix: use a merge patch:
```go
patch := client.MergeFrom(limaVM.DeepCopy())
limaVM.Spec.Running = app.Spec.Running
if err := r.Patch(ctx, limaVM, patch); err != nil { ... }
```

3. **Comment typo: "into to"** — `app_controller.go:177` (important, regression)

```go
// Propagate spec.running from App into to the LimaVM.
```

Fix: "into the LimaVM" (remove "to").

### Suggestions

1. **lima.yaml has only an amd64 image** — `lima.yaml:3` (suggestion, gap)

```yaml
images:
- location: https://...distro.v0.1.0.amd64.qcow2
```

The template provides no arm64 image. On Apple Silicon Macs with `vmType: qemu`, Lima will either fail to boot or fall back to emulation. If this template is temporary (per the comment in `controller.go:18`), this may be acceptable. Worth noting.

2. **Grammar in comment: "registers Lima types allows"** — `controller.go:63` (suggestion, regression)

```go
// It also registers Lima types allows App controller to create and watch LimaVM resources.
```

Fix: "It registers Lima types so the App controller can create and watch LimaVM resources."

---

## Testing Assessment

This change adds no tests — no unit tests, no BATS integration tests.

Untested scenarios ranked by risk:

1. **Happy path: App creation triggers LimaVM creation** — blocked by the critical stall bug above; once fixed, this is the most important scenario to cover.
2. **App deletion triggers LimaVM teardown and waits for completion** — the deletion flow has multiple branches (LimaVM gone, LimaVM exists but not deleting, LimaVM deleting). Each branch should be exercised.
3. **`spec.running` propagation from App to LimaVM** — changing `app.spec.running` should update `limaVM.spec.running`.
4. **Status condition mirroring** — LimaVM condition changes should appear on the App status.
5. **Re-creation after deletion** — deleting the App and creating a new one should produce a fresh LimaVM.

The Demo controller (a comparable singleton) has a comprehensive BATS test suite at `bats/tests/32-app-controllers/demo.bats` (295 lines). The App controller should follow the same pattern.

## Documentation Assessment

- `docs/development.md` MSYS2 section: already on main, no issues.
- `app_types.go` immutability comment on `Namespace`: clear and useful.
- The inline comments in the reconcile loop explain the "why" well, particularly the deletion comment block (lines 54-67) and the input ConfigMap lifecycle.
- The `lima.yaml` template is marked temporary (`controller.go:18`) but has no tracking issue or TODO for its removal.

---

### Codex GPT 5.4

### Executive Summary

5 commits: App controller lifecycle for LimaVM, MSYS2 BATS harness support, and a yq bump. The yq bump looks routine, but the App lifecycle change should not merge as-is: there is one merge-blocking reconcile bug that leaves fresh App creation stuck after the bootstrap ConfigMap, and the new status mirroring is not exact and can report stale conditions.

### Critical Issues

1. **Bootstrap reconcile never advances past the input ConfigMap** — `app_controller.go:117`, `app_controller.go:209` (critical, regression)

```go
if apierrors.IsNotFound(err) {
    ...
    if err := r.Create(ctx, inputCM); err != nil {
        return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
    }
    return ctrl.Result{}, nil // requeue; LimaVM creation happens next iteration
}
```

The comment is wrong: there is no next iteration here. I traced the full path through the same commit: the reconcile loop creates the bootstrap ConfigMap and returns successfully, but `SetupWithManager()` only watches `App` and owned `LimaVM` resources, not the bootstrap `ConfigMap`. That means a clean `App` create stops permanently at `ConfigMap/rd`; nothing requeues the controller to create `LimaVM/rd` until some unrelated `App` event happens later. `git blame` shows both the `return ctrl.Result{}, nil` and the `Owns(&limav1alpha1.LimaVM{})` watch were introduced by `7545302`.

Fix: either create the bootstrap ConfigMap and the LimaVM in the same reconcile, or explicitly `Requeue`/`RequeueAfter` after creating `ConfigMap/rd`, or add an `Owns(&corev1.ConfigMap{})` watch for that bootstrap object.

### Important Issues

1. **Mirroring only adds child conditions, so stale App conditions survive LimaVM recreation** — `app_controller.go:186`, `limavm_controller.go:175` (important, regression)

```go
statusChanged := false
for _, cond := range limaVM.Status.Conditions {
    statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
        Type: cond.Type,
        Status: cond.Status,
        Reason: cond.Reason,
        Message: cond.Message,
```

This does not actually "mirror" LimaVM conditions; it only upserts the conditions that currently exist on the child. I traced the child path: a newly created LimaVM initially sets only `Created=Unknown` and returns, with no `Running` condition yet. If the App previously mirrored `Running=True` from an older LimaVM, that stale `Running` condition remains on the App until some later reconcile happens to overwrite it. The same problem appears for any condition type the child stops publishing. `git blame` shows the mirror loop was introduced in `7545302`.

Fix: rebuild the mirrored subset from scratch on each reconcile. For example, clear the App conditions that are sourced from LimaVM and then repopulate them from `limaVM.Status.Conditions`, preserving only any App-native conditions if you add those later.

2. **The new App lifecycle has no automated coverage** — `app_controller.go:43` (important, gap)

This commit adds a multi-reconcile create path, finalizer-driven cross-controller delete path, and status mirroring, but it adds no unit or BATS coverage. The existing repo already uses BATS for comparable LimaVM lifecycle behavior in `bats/tests/33-lima-controllers/limavm-lifecycle.bats` and `bats/tests/33-lima-controllers/limavm-instance.bats`; meanwhile `bats/tests/32-app-controllers` still has only `demo.bats` and `passthrough.bats`. That gap is what let the stuck-bootstrap bug above slip through.

Fix: add App-controller coverage for initial App creation, LimaVM creation from the embedded template, `spec.running` propagation, deletion waiting for LimaVM finalizer cleanup, and App status mirroring during LimaVM create/delete/recreate.

### Testing Assessment

I did not run the suite; this assessment is from code inspection.

1. The highest-risk missing test is clean App bootstrap: create `App/app`, verify `ConfigMap/rd` is created, `LimaVM/rd` is created without any extra poke, and the bootstrap ConfigMap is then removed.
2. App deletion needs an integration test that verifies the App finalizer waits for LimaVM finalizer-based teardown and only then disappears.
3. Status mirroring needs coverage for LimaVM recreation and for intermediate states where LimaVM publishes only a subset of conditions.
4. Namespace immutability on `App.spec.namespace` needs at least one admission test.
5. The MSYS2 harness changes have no helper-level tests for `is_msys()`, `win_to_posix_exports()`, or the `rdd()` path conversion rules, despite this repo already having helper BATS tests under `bats/helpers`.

### Documentation Assessment

`docs/development.md` was updated appropriately for MSYS2. The App lifecycle change still lacks matching design or developer documentation for the new embedded Lima template, the fixed `rd` resource names, and the intended App-to-LimaVM create/delete/status contract.

---

### Gemini 3.1 Pro

### Executive Summary

The `app-controller-create-delete-limavm` branch introduces MSYS2 BATS test support, bumps dependencies, and heavily modifies the App controller to autonomously manage the `LimaVM` lifecycle. While the MSYS2 enhancements and dependency changes are solid, the core App controller lifecycle implementation contains critical regressions that will cause the controller to stall indefinitely during creation and leak resources on transient errors during teardown. The branch should merge only after the controller state machine and error handling issues are fixed.

### Critical Issues

1. **App controller lifecycle stalls indefinitely when creating input ConfigMap** — `app_controller.go:133-136` (critical, regression)

```go
			if err := r.Create(ctx, inputCM); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
			}
			return ctrl.Result{}, nil // requeue; LimaVM creation happens next iteration
```

The developer's intent was to immediately requeue after creating the `inputCM`. However, because the `App` reconciler does not `Owns(&corev1.ConfigMap{})` in `SetupWithManager`, no internal watch or event handler exists for it. Returning `ctrl.Result{}, nil` tells `controller-runtime` the reconciliation is complete. Without an event to trigger the next loop, the reconciler stalls here indefinitely, and the `LimaVM` is never created.

**Fix:** Return `ctrl.Result{Requeue: true}, nil` to force an immediate requeue, or add `.Owns(&corev1.ConfigMap{})` to the controller builder in `SetupWithManager`. Using `Requeue: true` is ideal since the ConfigMap is highly ephemeral.

2. **Error handling gap in deletion cleanup leads to orphaned ConfigMap** — `app_controller.go:76-85` (critical, regression)

**Consolidator note: downgraded to suggestion.** Gemini claims the ConfigMap
would be "orphaned forever," but the ConfigMap has an ownerReference to the
App (set at line 131). Kubernetes garbage collection deletes owned resources
when the owner is removed. The ConfigMap is not orphaned even if the
finalizer-removal path skips the explicit delete. This is sloppy error handling
but not a data-loss risk.

```go
		case apierrors.IsNotFound(err):
			// LimaVM is gone. Clean up the input ConfigMap if it still exists
			inputCM := &corev1.ConfigMap{}
			if cmErr := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM); cmErr == nil {
				// delete inputCM...
			} else if !apierrors.IsNotFound(cmErr) {
				log.Error(cmErr, "Failed to fetch input ConfigMap during cleanup")
			}
			return ctrl.Result{}, base.RemoveFinalizer(ctx, r.Client, &app)
```

If `r.Get` fails due to a transient API error, the error is logged and the
controller falls through to `base.RemoveFinalizer`. Returning the error
instead would trigger a retry.

### Important Issues

1. **Incorrect `ObservedGeneration` breaks API conventions for status conditions** — `app_controller.go:193-194` (important, regression)

```go
			Message:            cond.Message,
			ObservedGeneration: cond.ObservedGeneration,
			LastTransitionTime: cond.LastTransitionTime,
```

Copying `cond.ObservedGeneration` mirrors the underlying `LimaVM` object's `.metadata.generation`. Kubernetes API conventions strictly require that `ObservedGeneration` in a status condition must match the `.metadata.generation` of the object the status is attached to (the `App`). If the `App`'s generation diverges from the `LimaVM`'s, clients will mistakenly assume the `App` condition is out of date.

**Fix:** Bind the status generation to the `App` object: `ObservedGeneration: app.Generation`.

2. **Unnecessary polling for LimaVM deletion** — `app_controller.go:88-96` (important, gap)

**Consolidator note: downgraded to non-issue.** The `Owns` watch provides the
fast path; `RequeueAfter` is a standard defensive pattern against missed watch
events during controller restarts. The 2-second interval is reasonable.

3. **Ignored transient errors when cleaning up `inputCM` in steady state** — `app_controller.go:167-175` (important, gap)

```go
	if err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM); err == nil {
        // delete...
	} else if !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to fetch input ConfigMap")
	}
```

If fetching the leftover `inputCM` fails with a transient error after the
`LimaVM` is created, the error is logged but ignored. The stale ConfigMap
persists until an unrelated event triggers reconciliation.

### Testing Assessment

The MSYS2 framework updates are a positive addition, but there is a severe lack of test coverage for the complex orchestration in the new App controller.

**Untested Scenarios (ranked by risk):**
1. **Creation State Machine:** No tests verify that the multi-step `inputCM -> requeue -> LimaVM -> delete inputCM` sequence completes successfully without stalling (which would have caught Critical Issue 1).
2. **Deletion and Finalizer Cascading:** No tests ensure the `App` controller successfully holds its finalizer and waits for the `LimaVM` to completely disappear.
3. **Resilience to Transient API Errors:** No mocked unit tests assert that API errors during intermediate steps are safely retried rather than permanently swallowed.
4. **Status Mirroring:** No tests verify that `LimaVM` conditions correctly propagate to the `App` with the proper `ObservedGeneration`.

### Documentation Assessment

`docs/development.md` correctly outlines the MSYS2 warnings regarding `pacman`. However, there are no internal code comments or architecture diagrams explaining the complex lifecycle interplay between the App controller and the LimaVM webhook (specifically the `inputCM` temporary hand-off). Adding a brief structural outline to `app_controller.go` explaining why `base.DeleteOwnedResources()` was deliberately avoided would protect this code from accidental refactors by future maintainers.
