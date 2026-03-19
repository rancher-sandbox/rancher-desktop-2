# Deep Review: 20260313-120000-app-controller-create-delete-limavm

| | |
|---|---|
| **Date** | 2026-03-13 12:00 |
| **Branch** | `app-controller-create-delete-limavm` |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Rework** — DeleteOwnedResources strips LimaVM finalizer leaking VM on disk, hardcoded developer path in lima.yaml |

---

## 1. Consolidated Review

### Executive Summary

This branch wires the App controller to create a LimaVM and its input ConfigMap, propagate `spec.running`, and mirror status conditions back. The BATS/MSYS2 infrastructure changes and dependency bumps are clean.

The App controller lifecycle has two critical bugs that prevent merging: (1) `DeleteOwnedResources` strips the LimaVM's cleanup finalizer, leaking the VM on disk; (2) the embedded `lima.yaml` hardcodes a developer-specific path (`HOST_HOME: /Users/ninok`). The reconcile ordering also causes unnecessary ConfigMap churn. **Go back for rework.**

### Critical Findings

**[CRITICAL] app_controller.go:58 — DeleteOwnedResources strips LimaVM finalizer, leaking VM on disk** [Codex GPT 5.4, Gemini 3.1 Pro]

Problem: `base.DeleteOwnedResources()` removes the `rdd.rancherdesktop.io/cleanup` finalizer from all owned resources before deleting them. The LimaVM controller relies on this same finalizer to run `handleDeletion()`, which stops the VM process and removes instance files from disk. Stripping the finalizer first lets Kubernetes hard-delete the LimaVM object, bypassing cleanup entirely. The VM instance persists on disk with no controller to remove it.

Verified by tracing the call chain: `DeleteOwnedResources` (finalizer.go:134-150) -> `controllerutil.RemoveFinalizer(&item, FinalizerName)` -> `c.Delete(ctx, &item)`. The LimaVM controller's `handleDeletion` (limavm_lifecycle.go:56-78) never runs because the finalizer is already gone.

Fix: Do not use `DeleteOwnedResources` for LimaVM. Instead, issue a direct `r.Delete(ctx, limaVM)` and requeue. Remove the App finalizer only after `r.Get` for the LimaVM returns `IsNotFound`, confirming the LimaVM controller completed its own cleanup.

---

**[CRITICAL] lima.yaml:37 — Hardcoded developer home directory** [Claude Opus 4.6, Gemini 3.1 Pro]

Problem: `HOST_HOME: /Users/ninok` is baked into the embedded template via `//go:embed`. Every binary built from this branch sends the wrong path to every machine except the original developer's. This breaks volume mounts and host-agent integrations.

Fix: Templatize this field (e.g., `{{.Home}}` like the `hostSocket` line already does) or remove it if the Lima guest agent discovers the host home directory at runtime.

---

**[CRITICAL] app_controller.go:73-88 — ConfigMap recreation on every status-triggered reconcile** [Gemini 3.1 Pro]

Problem: In steady state the input ConfigMap is deleted (line 121-126). When a LimaVM status update triggers the next reconcile, the controller checks for the input ConfigMap first (line 73), finds it missing, recreates it, and returns early (line 88). Status mirroring (lines 136-151) never runs on that cycle. The next reconcile deletes the ConfigMap again and proceeds to status mirroring. This doubles reconcile cycles and delays status propagation.

This is not an infinite loop (as Gemini originally claimed) but does cause unnecessary churn and missed status updates.

Fix: Check for the LimaVM first. If it exists, skip ConfigMap creation and proceed directly to status mirroring. Only create the ConfigMap when the LimaVM does not yet exist.

### Important Findings

**[IMPORTANT] app_controller.go:73-88 — Input ConfigMap has no owner reference** [Claude Opus 4.6]

Problem: The input ConfigMap is created without an owner reference to the App. If the App is deleted between ConfigMap creation (reconcile 1) and LimaVM creation (reconcile 2), `DeleteOwnedResources` cannot find or clean up the ConfigMap. It remains orphaned.

Fix: Set a controller reference on the input ConfigMap before creating it, the same way the LimaVM gets one at line 109.

---

**[IMPORTANT] app_controller.go:71 — Mutable namespace field leaks resources** [Codex GPT 5.4]

Problem: `app.spec.namespace` has no immutability constraint. Changing it after the LimaVM exists causes the controller to look for resources in the new namespace, find nothing, and bootstrap a fresh ConfigMap there. The old LimaVM in the original namespace becomes orphaned. On App deletion, cleanup searches only the new namespace, so the old VM persists.

Fix: Add a CEL immutability rule on `spec.namespace` (e.g., `rule: "self == oldSelf"`) or persist the managed namespace in status and handle migration explicitly.

---

**[IMPORTANT] app_controller.go:136-144 — Condition mirroring stamps misleading ObservedGeneration** [Claude Opus 4.6, Codex GPT 5.4]

Problem: The mirror logic sets `ObservedGeneration: app.Generation` on copied conditions. After an App spec update, a reconcile may run before the LimaVM controller processes the change. The App's conditions then claim the current generation was observed when the child status still reflects the prior generation.

Fix: Either omit `ObservedGeneration` until confirmed current, or propagate the LimaVM's own `ObservedGeneration` if it tracks one.

---

**[IMPORTANT] app_controller.go:136-144 — Condition mirroring drops LastTransitionTime** [Claude Opus 4.6]

Problem: The mirrored `metav1.Condition` omits `LastTransitionTime`. `apimeta.SetStatusCondition` fills zero-valued timestamps with `metav1.Now()`, so the App's conditions reflect when the App controller copied the condition, not when the LimaVM actually transitioned. This matters for the stale-status-after-restart fix, which compares `lastTransitionTime`.

Fix: Copy `cond.LastTransitionTime` into the mirrored condition struct.

---

**[IMPORTANT] app_controller.go:37-38 — Dead code: Recorder field and condition constants** [Claude Opus 4.6]

Problem: The `Recorder` field, the `events` import, and the `ConditionCreated`/`ConditionRunning` constants are unused after the `setCondition` method was removed. The LimaVM controller defines its own condition constants.

Fix: Remove the dead field, import, and constants.

### Suggestions

**[SUGGESTION] controller.go:63 — Grammar error in comment** [Claude Opus 4.6, Gemini 3.1 Pro]

"It also registers Lima types allows App controller" is ungrammatical.

Fix: "It also registers Lima types, allowing the App controller to create and watch LimaVM resources."

---

**[SUGGESTION] app_controller.go:121 — Non-NotFound errors silently ignored on ConfigMap re-fetch** [Claude Opus 4.6]

Problem: The `if err == nil` check silently skips deletion when `Get` fails for transient reasons. Low impact since the ConfigMap is inert, but inconsistent with the error handling elsewhere.

Fix: Log non-NotFound errors for observability.

### Testing Assessment

No tests were added for any new behavior. The branch adds zero Go unit tests and zero BATS integration tests for the App controller's LimaVM lifecycle.

Untested scenarios:
1. ConfigMap creation on first reconcile
2. LimaVM creation with correct owner reference and templateRef
3. Input ConfigMap deletion after LimaVM exists
4. `spec.running` propagation from App to LimaVM
5. Condition mirroring from LimaVM status to App status
6. App deletion cleanup (finalizer, VM cleanup on disk)
7. Idempotency: reconcile converges when everything matches desired state
8. Edge case: App deleted between ConfigMap and LimaVM creation
9. Edge case: namespace change after initial bootstrap

Existing tests in `bats/tests/32-app-controllers/` (demo, passthrough) establish a testing convention this controller should follow.

### Documentation Assessment

- The `controller.go:63` comment has a grammar error (noted above).
- The `lima.yaml` file carries no comment explaining what the hardcoded values represent or how they should be replaced.
- No architectural documentation describes the lifecycle flow between AppReconciler, the LimaVM mutating webhook that consumes the ConfigMap, and the LimaVMReconciler.

---

## 2. Agent Performance Retro

### Claude Opus 4.6

- **Unique contributions:** Dead code (Recorder field, condition constants), LastTransitionTime drop, input ConfigMap missing owner reference, silent error swallowing on re-fetch
- **Accuracy:** All findings verified. No false positives.
- **Depth:** Did not investigate the DeleteOwnedResources finalizer stripping bug (the most critical finding). Focused on the code within the diff rather than tracing cross-controller interactions.
- **Signal-to-noise:** High signal. Every finding is actionable. Good balance of severity levels.

### Codex GPT 5.4

- **Unique contributions:** Namespace mutability leak, ConfigMap/LimaVM name collision risk, pre-existing resource adoption
- **Accuracy:** The DeleteOwnedResources finding is confirmed. The namespace mutability finding is confirmed. The name collision concern is valid but low risk in this embedded-kube context.
- **Depth:** Best cross-controller analysis. Traced the finalizer stripping through `DeleteOwnedResources` into `limavm_lifecycle.go`. Also analyzed namespace mutability implications.
- **Signal-to-noise:** Good signal, though the name collision finding is somewhat theoretical given the embedded kube-apiserver context.

### Gemini 3.1 Pro

- **Unique contributions:** Identified the ConfigMap recreation churn pattern (though overstated as "infinite loop")
- **Accuracy:** The "infinite ConfigMap creation loop" was overstated — it is not infinite, but the underlying concern (unnecessary churn, delayed status mirroring) is valid. The finalizer stripping finding matches Codex.
- **Depth:** Moderate. Identified the reconcile ordering issue but did not fully trace the loop to confirm it terminates.
- **Signal-to-noise:** Fewer findings overall. The infinite loop overstatement reduces trust.

### Summary Table

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Duration (s) | 278 | 268 | 250 |
| Critical findings | 1 | 2 | 3 |
| Important findings | 4 | 3 | 0 |
| Suggestions | 2 | 0 | 1 |
| False positives | 0 | 0 | 1 (severity overstatement) |
| Unique insights | 4 | 3 | 1 |

*Agents ran concurrently and competed for resources; durations are approximate.*

**Overall assessment:** Codex GPT 5.4 provided the most valuable review by identifying the highest-severity bug (finalizer stripping) and the confirmed namespace mutability issue. Claude Opus 4.6 had the broadest coverage and zero false positives but missed the most critical cross-controller bug. Gemini 3.1 Pro identified a valid reconcile ordering issue but overstated its severity. The three agents together cover more ground than any one alone — each caught findings the others missed.

---

## 3. Skill Improvement Recommendations

- The review prompt should explicitly instruct agents to trace cross-controller interactions, not just analyze the diff in isolation. Claude missed the most critical bug because it stayed within the changed files.
- The prompt should ask agents to distinguish "theoretical concerns in a full Kubernetes cluster" from "realistic concerns in an embedded single-user kube-apiserver." Several findings (name collision, resource adoption) are less relevant in the embedded context.
- Consider adding a "verify your critical findings" step where agents must show the exact code path they traced, reducing severity overstatements like Gemini's "infinite loop."
- All three agents noted the missing tests. The prompt could ask for specific test scenarios ranked by risk, to help prioritize test writing.

---

## 4. Appendix: Original Reviews

### Claude Opus 4.6

---

## Executive Summary

This change wires the App controller to create a LimaVM and its input ConfigMap, propagate the `running` state, and mirror status conditions back. The controller logic is structurally sound, but the embedded `lima.yaml` contains a hardcoded developer-specific path (`HOST_HOME: /Users/ninok`) that would ship in every build. Additionally, there are no tests for any of the new behavior. I'd recommend fixing the hardcoded path and the dead code before merging; tests can potentially follow in a separate PR if that's the project convention, but the gap should be acknowledged.

---

## Findings

### **[CRITICAL] lima.yaml:38 — Hardcoded developer home directory**

**Classification:** regression (introduced by `cf27c08`)

**Problem:** `HOST_HOME: /Users/ninok` is a developer-specific macOS path baked into the embedded template. This value ends up in the ConfigMap that configures every LimaVM instance. On any machine other than ninok's, this path is wrong. Even though the file comment says "temporary", this ships in the binary via `//go:embed`.

**Fix:** Either templatize this field (e.g., `{{.Home}}` like the `hostSocket` line already does) or remove it if the Lima guest agent can discover the host home directory at runtime.

---

### **[IMPORTANT] app_controller.go:73-88 — Input ConfigMap has no owner reference; orphaned on early App deletion**

**Classification:** gap

**Problem:** The input ConfigMap `"rd"` is created without an owner reference pointing to the App. If the App is deleted between ConfigMap creation (reconcile 1) and LimaVM creation (reconcile 2), `DeleteOwnedResources` at line 58 won't find the ConfigMap because `IsOwnedByUID` checks `ownerReferences`. The ConfigMap is orphaned indefinitely.

**Fix:** Set a controller reference on the input ConfigMap before creating it:
```go
if err := ctrl.SetControllerReference(&app, inputConfigMap, r.Scheme); err != nil {
    return ctrl.Result{}, fmt.Errorf("failed to set owner reference on input ConfigMap: %w", err)
}
```
This ensures `DeleteOwnedResources` catches it during App cleanup.

---

### **[IMPORTANT] app_controller.go:37 — `Recorder` field is dead code**

**Classification:** regression (introduced by `cf27c08`)

**Problem:** The old `setCondition` method that used `r.Recorder` was removed by this change. The `Recorder` field and its `events` import (`k8s.io/client-go/tools/events`) are now dead code. Go compiles it without error since the import is used in the field type declaration, but nothing ever calls `r.Recorder`.

**Fix:** Remove the `Recorder` field and the `events` import. If events are needed later, add them back then.

---

### **[IMPORTANT] app_controller.go:30-31 — `ConditionCreated` and `ConditionRunning` constants are dead code**

**Classification:** regression (introduced by `cf27c08`)

**Problem:** These exported constants were used by the old `setCondition` call. The new code mirrors conditions from LimaVM and never references these constants. The LimaVM controller defines its own identical constants. No external package references the App controller's copies.

**Fix:** Remove both constants.

---

### **[IMPORTANT] app_controller.go:136-144 — Condition mirroring drops `LastTransitionTime`**

**Classification:** regression (introduced by `cf27c08`)

**Problem:** When mirroring LimaVM conditions to App status, `LastTransitionTime` is omitted from the `metav1.Condition` struct. `apimeta.SetStatusCondition` fills in `metav1.Now()` for any zero-valued `LastTransitionTime`. This means the App's conditions reflect when the App controller *copied* the condition, not when the LimaVM actually transitioned.

**Fix:** Carry over the original transition time:
```go
statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
    Type:               cond.Type,
    Status:             cond.Status,
    Reason:             cond.Reason,
    Message:            cond.Message,
    LastTransitionTime: cond.LastTransitionTime,
    ObservedGeneration: app.Generation,
}) || statusChanged
```

---

### **[SUGGESTION] controller.go:63 — Grammar in comment**

**Classification:** regression

**Problem:** "It also registers Lima types allows App controller to create and watch LimaVM resources." — missing comma and wrong verb form.

**Fix:** "It also registers Lima types, allowing the App controller to create and watch LimaVM resources."

---

### **[SUGGESTION] app_controller.go:121 — Non-NotFound errors on re-fetch silently ignored**

**Classification:** regression

**Problem:** The `if err == nil` check at line 121 silently skips ConfigMap deletion when the Get fails for reasons other than NotFound. Low impact since the ConfigMap is inert, but inconsistent with the error handling elsewhere.

**Fix:** Log non-NotFound errors.

---

## Testing Assessment

**No tests exist for any of the new behavior.** There are:
- Zero Go unit tests in `pkg/controllers/app/`
- Zero BATS integration tests for the App controller's LimaVM lifecycle

Untested scenarios introduced by this change:
1. ConfigMap creation on first reconcile
2. LimaVM creation with correct owner reference and templateRef
3. Input ConfigMap deletion after LimaVM exists
4. `spec.running` propagation from App to LimaVM
5. Condition mirroring from LimaVM status to App status
6. App deletion cleanup (finalizer removal, owned resource deletion)
7. Idempotency: reconcile converges when everything is already in desired state
8. Edge cases: App deleted before LimaVM created, LimaVM creation fails mid-reconcile

---

## Documentation Assessment

- The `controller.go:63` comment has a grammar issue (noted above).
- The `lima.yaml` comment says "temporary" which is appropriate — but the file itself contains no comment explaining what the hardcoded values represent.
- No documentation updates are needed for `docs/development.md` beyond what's already in the diff.

---

### Codex GPT 5.4

**Executive Summary**

Against `upstream/main`, this branch tip only changes the App controller and embedded Lima template; the MSYS2/doc/dependency items in the request are already on `main`. The lifecycle work is not safe to merge as-is: I verified two critical regressions in deletion/bootstrap handling, plus a few important ownership/state-management bugs that need fixes before this should ship.

**Findings**

**CRITICAL** app_controller.go:58 — App deletion bypasses LimaVM teardown
Classification: regression
Problem: On App deletion, the new controller calls `base.DeleteOwnedResources()`. That helper strips the child resource's `rdd.rancherdesktop.io/cleanup` finalizer before deleting it (finalizer.go:134), but `LimaVM` relies on that finalizer to run `handleDeletion()` and remove the actual Lima instance plus owned template ConfigMaps (limavm_lifecycle.go:32). I traced the full chain: App deletion now deletes the API object without giving the LimaVM controller a chance to clean up the VM on disk.
Fix: Do not use the generic owned-resource deleter for `LimaVM`. Delete the owned `LimaVM` normally and wait for it to finish its own finalizer-driven teardown before removing the App finalizer, or teach `DeleteOwnedResources()` to preserve finalizers for resources with controller-specific cleanup.

**CRITICAL** app_controller.go:73, app_controller.go:121 — Any pre-existing `ConfigMap/rd` is treated as bootstrap input and then deleted
Classification: regression
Problem: The controller hard-codes `inputConfigMapName = "rd"` and, if that ConfigMap already exists, assumes it is its bootstrap input. It then feeds that object into the LimaVM admission flow and later unconditionally deletes it. There is no ownership/label check anywhere on this path. In a namespace that already contains an unrelated `ConfigMap/rd`, the App controller will boot from someone else's data and then delete that ConfigMap. I confirmed the introduced lines with `git blame`; this behavior is entirely from this commit.
Fix: Use an App-owned unique name derived from App UID/generation, add an ownership marker, and only reuse/delete ConfigMaps that carry that marker.

**IMPORTANT** app_controller.go:93, app_controller.go:128 — The controller silently adopts an unrelated `LimaVM/rd`
Classification: regression
Problem: If a `LimaVM` named `rd` already exists in the target namespace, the controller assumes it is the managed child. It never checks owner refs or template provenance before mutating `spec.running` and mirroring its conditions into App status.
Fix: Verify the child is controlled by this App before mutating it.

**IMPORTANT** app_controller.go:71 — Changing `app.spec.namespace` leaks and wedges managed resources
Classification: regression
Problem: The new lifecycle code derives the working namespace from the current `app.spec.namespace` on every reconcile. Because that field is mutable, changing it makes the controller stop managing the old child, bootstrap a new ConfigMap in the new namespace, and then fail creation because LimaVM names are cluster-unique. On App deletion, cleanup only searches the new namespace, so the old VM is left behind.
Fix: Make `spec.namespace` immutable once the App exists, or persist the managed namespace in status and explicitly migrate/delete old resources before switching.

**IMPORTANT** app_controller.go:136 — Mirrored App conditions can claim the current generation was observed when it was not
Classification: regression
Problem: The mirror logic copies LimaVM conditions but overwrites `ObservedGeneration` with `app.Generation`. After an App spec update, a reconcile can run before the LimaVM controller has processed the new desired state, yet App conditions will already look current.
Fix: Do not stamp `app.Generation` here. Propagate a real observed generation from the child once LimaVM conditions record one, or leave `ObservedGeneration` unset.

**Testing Assessment**

No unit tests or BATS coverage were added for the new App lifecycle. At minimum, this branch needs tests for App deletion cleaning up the on-disk Lima instance, collisions with pre-existing ConfigMap/rd and LimaVM/rd, spec.namespace mutation, bootstrap ConfigMap cleanup on failure/deletion races, and status mirroring after spec.running changes.

**Documentation Assessment**

There is no documentation for the new App-owned LimaVM lifecycle, its reserved child object names, or the namespace-mutation constraints this implementation needs.

---

### Gemini 3.1 Pro

### Executive Summary

This PR implements the initial `App` controller lifecycle to provision and manage a `LimaVM` instance, as well as several testing infrastructure improvements for MSYS2/WSL. While the intention is correct, the PR **must go back for rework** due to critical flaws in the reconciliation loop and resource cleanup logic. Specifically, the cleanup routine permanently leaks the Lima VM on disk, the controller loop endlessly recreates and deletes the input ConfigMap (silently dropping status updates), and the embedded `lima.yaml` contains a hardcoded developer path.

### Findings

**[CRITICAL] pkg/controllers/app/app/controllers/app_controller.go:49-53 — `DeleteOwnedResources` strips LimaVM finalizer**
- **Classification:** Regression
- **Problem:** The `AppReconciler` uses `base.DeleteOwnedResources` to clean up the `LimaVM` before removing its own finalizer. However, `DeleteOwnedResources` (by design) strips the shared `base.FinalizerName` (`rdd.rancherdesktop.io/cleanup`) from all owned resources before issuing a delete. Since `LimaVM` relies on this exact finalizer to trigger its own `handleDeletion` logic (which deletes the physical VM files from disk), stripping the finalizer causes K8s to hard-delete the `LimaVM` object instantly. The `LimaVMReconciler` is bypassed, and the VM instance is leaked permanently on disk.
- **Fix:** Do not use `base.DeleteOwnedResources` here. Instead, issue a direct `r.Delete(ctx, limaVM)` and wait for the `LimaVM` to disappear by requeuing. Only remove the `App` finalizer once `r.Get` for the `LimaVM` returns `IsNotFound`.

**[CRITICAL] pkg/controllers/app/app/controllers/app_controller.go:66-83 — Infinite ConfigMap creation loop drops status mirroring**
- **Classification:** Regression
- **Problem:** The reconciler checks for `inputConfigMap` and unconditionally recreates it if missing, then returns `nil`. In the steady state, the `LimaVM` exists and the `inputConfigMap` was successfully deleted. When a `LimaVM` status update triggers a new reconcile, the controller sees the `inputConfigMap` is missing, recreates it, and returns early. Because it returns, the status mirroring logic at the bottom of the function is never reached, effectively swallowing `LimaVM` status updates.
- **Fix:** Reorder the logic. Check for `LimaVM` existence *first*. If `LimaVM` exists, proceed directly to deleting the `inputConfigMap` (if present) and mirroring the status. Only create the `inputConfigMap` if `LimaVM` is `IsNotFound`.

**[CRITICAL] pkg/controllers/app/app/lima.yaml:37 — Hardcoded user path in embedded template**
- **Classification:** Regression
- **Problem:** The template contains `HOST_HOME: /Users/ninok`. Because this file is embedded into the binary via `//go:embed lima.yaml`, this developer-specific path will be shipped to all users.
- **Fix:** Replace the hardcoded `/Users/ninok` with proper runtime templating or rely on the native environment variable resolution for `HOST_HOME`.

**[SUGGESTION] pkg/controllers/app/app/controller.go:62 — Grammatical error in comment**
- **Classification:** Enhancement
- **Problem:** The comment `It also registers Lima types allows App controller to create...` is grammatically incorrect.
- **Fix:** Change to `It also registers Lima types to allow the App controller to create...`

### Testing Assessment

There is a significant gap in test coverage. The PR does not add any tests to verify the `App` controller's behavior.

### Documentation Assessment

The MSYS2 environment setup is well-documented in `docs/development.md`. However, there is no architectural documentation outlining the lifecycle flow between the `AppReconciler`, the `LimaVM` mutating webhook, and the `LimaVMReconciler`.
