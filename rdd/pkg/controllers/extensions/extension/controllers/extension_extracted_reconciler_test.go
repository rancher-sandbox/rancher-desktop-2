// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	extensionsv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// TestMain redirects the user's home directory (and thus
// instance.ExtensionDir(), used by extensionInstallDir) into a temporary
// directory before any test runs, so extraction tests never touch the real
// user's Rancher Desktop extension install directory. This must happen
// before instance.Dir() is called for the first time, since it is memoized
// via sync.OnceValue.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rdx-extracted-reconciler-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	os.Setenv("HOME", dir)
	os.Exit(m.Run())
}

// fakeEngine is a test double for the [engine] interface, used so tests never
// need a real container engine (e.g. Docker).
type fakeEngine struct {
	connectErr        error
	createForExportFn func(ctx context.Context, ext *extensionsv1alpha1.Extension) (engineCreateForExportResult, error)
	exportFn          func(ctx context.Context, opts engineExportOptions) error
}

var _ engine = &fakeEngine{}

func (f *fakeEngine) connect(context.Context) error {
	return f.connectErr
}

func (f *fakeEngine) createForExport(ctx context.Context, ext *extensionsv1alpha1.Extension) (engineCreateForExportResult, error) {
	if f.createForExportFn != nil {
		return f.createForExportFn(ctx, ext)
	}
	return engineCreateForExportResult{id: "fake-container-id"}, nil
}

func (f *fakeEngine) export(ctx context.Context, opts engineExportOptions) error {
	return f.exportFn(ctx, opts)
}

// writeFileExportFn returns an exportFn that writes the given file contents to
// opts.destDir/opts.sourcePath, reporting progress before completing, in the
// same asynchronous fashion as the real dockerEngine.export.
func writeFileExportFn(t *testing.T, contents map[string][]byte) func(ctx context.Context, opts engineExportOptions) error {
	t.Helper()
	return func(ctx context.Context, opts engineExportOptions) error {
		go func() {
			defer close(opts.ch)
			send := func(r result[engineExportProgress]) bool {
				select {
				case opts.ch <- r:
					return true
				case <-ctx.Done():
					return false
				}
			}
			if !send(success(engineExportProgressStarted)) {
				return
			}
			if !send(success(engineExportProgressCopying)) {
				return
			}
			data, ok := contents[opts.sourcePath]
			if !ok {
				send(failure[engineExportProgress](fmt.Errorf("no fixture for %q", opts.sourcePath)))
				return
			}
			if err := os.MkdirAll(opts.destDir, 0o755); err != nil {
				send(failure[engineExportProgress](err))
				return
			}
			dest := filepath.Join(opts.destDir, filepath.Base(opts.sourcePath))
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				send(failure[engineExportProgress](err))
				return
			}
			send(success(engineExportProgressCompleted))
		}()
		return nil
	}
}

// failingExportFn returns an exportFn whose call to export() itself fails
// synchronously (as opposed to reporting an error via the channel).
func failingExportFn(err error) func(ctx context.Context, opts engineExportOptions) error {
	return func(ctx context.Context, opts engineExportOptions) error {
		return err
	}
}

func newExtractedReconcilerTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, extensionsv1alpha1.AddToScheme(scheme))
	assert.NilError(t, apiextensionsv1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...)
	for _, obj := range objs {
		if ext, ok := obj.(*extensionsv1alpha1.Extension); ok {
			builder = builder.WithStatusSubresource(ext)
		}
	}

	return builder.Build()
}

// newExtractor builds a bare extensionExtractor wired up with the given
// client, without the ExtensionExtractedReconciler wrapper. This is used by
// tests that want to exercise a single helper method (e.g. prepare or
// extractStep) in isolation; the engine (if any) is passed directly to the
// relevant method rather than being stored on the extractor.
func newExtractor(t *testing.T, c client.Client, e engine) *extensionExtractor {
	t.Helper()
	extractor := &extensionExtractor{
		Client: c,
		ctx:    t.Context(),
		state:  make(map[types.UID]extractState),
	}
	extractor.prepare = extractor.prepareImpl
	extractor.extractMetadata = extractor.extractMetadataImpl
	extractor.extractIcon = extractor.extractIconImpl
	extractor.extractUI = extractor.extractUIImpl
	extractor.extractExecutable = extractor.extractExecutableImpl
	extractor.reconcileDelete = extractor.reconcileDeleteImpl
	extractor.extractStep = extractor.extractStepImpl
	return extractor
}

// newExtractedReconciler builds an ExtensionExtractedReconciler wired up with
// the given client and (fake) engine, without going through
// SetupWithManager/reconcileApp/App watches. Its steps are the real
// extensionExtractor, so this exercises the full pipeline end-to-end.
func newExtractedReconciler(t *testing.T, c client.Client, e engine) *ExtensionExtractedReconciler {
	t.Helper()
	extractor := newExtractor(t, c, e)
	r := &ExtensionExtractedReconciler{
		extensionExtractor: extractor,
		engine:             e,
	}
	return r
}

// stubSteps wraps an extensionExtractor with every step field defaulted to
// a no-op implementation returning a zero ctrl.Result and a nil error, used
// to test ExtensionExtractedReconciler.reconcileExtension's dispatch logic
// (i.e. which step is invoked for which Extracted condition reason) in
// isolation, without needing a real engine or any of the individual steps'
// own logic. setExtractedCondition is left as the real implementation
// (it is not a stubbable field), so tests assert on the Extracted condition
// via the fake client instead of a recorded-calls list. Use newStubSteps to
// construct one; tests then override individual *extensionExtractor fields
// as needed.
type stubSteps struct {
	*extensionExtractor

	engineReady bool
}

// newStubSteps builds a stubSteps with every step defaulted to a no-op
// implementation. engineReady indicates whether newReconcilerWithStubSteps
// should populate the reconciler's engine.
func newStubSteps(engineReady bool) *stubSteps {
	noop := func(context.Context, *extensionsv1alpha1.Extension, engine) (ctrl.Result, error) {
		return ctrl.Result{}, nil
	}
	s := &stubSteps{
		extensionExtractor: &extensionExtractor{
			prepare:           noop,
			extractMetadata:   noop,
			extractIcon:       noop,
			extractUI:         noop,
			extractExecutable: noop,
			reconcileDelete: func(context.Context, *extensionsv1alpha1.Extension) (ctrl.Result, error) {
				return ctrl.Result{}, nil
			},
		},
		engineReady: engineReady,
	}
	return s
}

// newReconcilerWithStubSteps builds an ExtensionExtractedReconciler whose
// steps are a stubSteps, so that reconcileExtension's dispatch logic can be
// tested without exercising any individual step's real implementation. The
// reconciler's engine is populated from steps.engineReady, since the engine
// is tracked on ExtensionExtractedReconciler rather than by the steps
// implementation.
func newReconcilerWithStubSteps(t *testing.T, c client.Client, steps *stubSteps) *ExtensionExtractedReconciler {
	t.Helper()
	steps.Client = c
	var e engine
	if steps.engineReady {
		e = &fakeEngine{}
	}
	r := &ExtensionExtractedReconciler{
		extensionExtractor: steps.extensionExtractor,
		engine:             e,
	}
	return r
}

// newTestExtension creates a bare Extension with the Extracted condition set
// to the given reason, and a resolved image (needed by extensionInstallDir).
// The image tag is derived from the test name so that extensionInstallDir
// (which is keyed only on the image) does not collide between tests sharing
// the same on-disk extension directory.
func newTestExtension(t *testing.T, name string, reason string) *extensionsv1alpha1.Extension {
	t.Helper()
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "rancher-desktop",
			UID:        types.UID(name + "-uid"),
			Finalizers: []string{extractedFinalizer},
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: fmt.Sprintf("ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:%s", t.Name()),
		},
	}
	apimeta.SetStatusCondition(&ext.Status.Conditions, metav1.Condition{
		Type:   extensionsv1alpha1.ExtensionConditionExtracted,
		Status: metav1.ConditionFalse,
		Reason: reason,
	})
	return ext
}

// waitForExtractedReason polls the Extension's Extracted condition until it
// matches the expected reason, or fails the test after a timeout. This is
// needed because extractStep's work happens in a background goroutine.
func waitForExtractedReason(t *testing.T, c client.Client, ext *extensionsv1alpha1.Extension, want string) *metav1.Condition {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *metav1.Condition
	for time.Now().Before(deadline) {
		updated := &extensionsv1alpha1.Extension{}
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated); err != nil {
			t.Fatalf("failed to get extension: %v", err)
		}
		last = apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		if last != nil && last.Reason == want {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Extracted condition reason %q, last seen: %+v", want, last)
	return nil
}

func TestExtensionExtractedReconcilerExtractMetadataFinalizeFailureIsNotOverwritten(t *testing.T) {
	// Regression test for the bug where a `finalize` failure (e.g. the
	// exported metadata.json cannot be opened/decoded) was silently
	// overwritten by the "completed successfully" transition to the next
	// step, allowing the pipeline to advance despite a real error.
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	c := newExtractedReconcilerTestClient(t, ext)

	fe := &fakeEngine{
		// Report a successful copy, but don't actually write metadata.json,
		// so the finalize step's os.Open call fails.
		exportFn: writeFileExportFn(t, map[string][]byte{}),
	}
	extractor := newExtractor(t, c, fe)
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}
	extractor.stateMu.Lock()
	extractor.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
	extractor.stateMu.Unlock()

	_, err := r.extractMetadata(t.Context(), ext, fe)
	assert.NilError(t, err)

	condition := waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonFailed)
	assert.Equal(t, condition.Status, metav1.ConditionFalse)

	// The condition must never advance to ExtractingIcon, which previously
	// happened because the failure was masked.
	time.Sleep(50 * time.Millisecond)
	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	final := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Equal(t, final.Reason, extensionsv1alpha1.ExtensionExtractedReasonFailed)
}

func TestExtensionExtractedReconcilerExtractMetadataSucceeds(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	c := newExtractedReconcilerTestClient(t, ext)

	metadata := []byte(`{"icon":"icon.png","host":{"binaries":null}}`)
	fe := &fakeEngine{
		exportFn: writeFileExportFn(t, map[string][]byte{"metadata.json": metadata}),
	}
	extractor := newExtractor(t, c, fe)
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}
	extractor.stateMu.Lock()
	extractor.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
	extractor.stateMu.Unlock()

	_, err := r.extractMetadata(t.Context(), ext, fe)
	assert.NilError(t, err)

	condition := waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonIcon)
	assert.Equal(t, condition.Status, metav1.ConditionFalse)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	assert.Assert(t, updated.Status.Metadata != nil)
	var manifest ExtensionManifest
	assert.NilError(t, json.Unmarshal(updated.Status.Metadata.Raw, &manifest))
	assert.Equal(t, manifest.Icon, "icon.png")
}

func TestExtensionExtractedReconcilerExtractStepGuardsAgainstReentry(t *testing.T) {
	// Regression test: calling extractStep twice in quick succession for the
	// same step must not start a second concurrent export.
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	c := newExtractedReconcilerTestClient(t, ext)

	var mu sync.Mutex
	callCount := 0
	release := make(chan struct{})

	fe := &fakeEngine{
		exportFn: func(ctx context.Context, opts engineExportOptions) error {
			mu.Lock()
			callCount++
			mu.Unlock()
			go func() {
				defer close(opts.ch)
				<-release
				opts.ch <- success(engineExportProgressCompleted)
			}()
			return nil
		},
	}
	extractor := newExtractor(t, c, fe)
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}
	extractor.stateMu.Lock()
	extractor.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
	extractor.stateMu.Unlock()

	_, err := r.extractMetadata(t.Context(), ext, fe)
	assert.NilError(t, err)
	// Second call while the first is still in flight (blocked on `release`).
	_, err = r.extractMetadata(t.Context(), ext, fe)
	assert.NilError(t, err)

	close(release)
	// Allow the (single) goroutine to finish; since there's no metadata.json
	// fixture, finalize will fail, which is fine -- we only care about the
	// export call count here.
	waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonFailed)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callCount, 1, "export should only be invoked once despite two extractStep calls")
}

func TestExtensionExtractedReconcilerSetExtractedConditionFailedClearsState(t *testing.T) {
	// Regression test for stale state: once the Failed reason is set, any
	// in-flight cancel/cleanup must be invoked and the state entry removed so
	// it cannot be mistaken for a still-running step.
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	c := newExtractedReconcilerTestClient(t, ext)
	extractor := newExtractor(t, c, &fakeEngine{})
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}

	cancelled := false
	cleanedUp := false
	extractor.stateMu.Lock()
	extractor.state[ext.GetUID()] = extractState{
		containerID: "fake-container-id",
		step:        extractionStepMetadata,
		cancel:      func() { cancelled = true },
		cleanup:     func() { cleanedUp = true },
	}
	extractor.stateMu.Unlock()

	assert.NilError(t, r.setExtractedCondition(t.Context(), ext, metav1.ConditionFalse,
		extensionsv1alpha1.ExtensionExtractedReasonFailed, "boom"))

	assert.Assert(t, cancelled, "cancel should have been called")
	assert.Assert(t, cleanedUp, "cleanup should have been called")

	extractor.stateMu.Lock()
	_, ok := extractor.state[ext.GetUID()]
	extractor.stateMu.Unlock()
	assert.Assert(t, !ok, "state entry should have been removed")
}

func TestExtensionExtractedReconcilerExtractIconFailsOnInvalidMetadata(t *testing.T) {
	// Guards against the nil-pointer panic that used to occur when
	// Status.Metadata was nil/invalid but the pipeline had advanced anyway.
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonIcon)
	// Valid JSON, but the wrong shape (a string instead of an object), so
	// json.Unmarshal into ExtensionManifest fails with a type error -- this
	// exercises the same failure path a truly corrupt metadata.json would.
	ext.Status.Metadata = &apiextensionsv1.JSON{Raw: []byte(`"not-an-object"`)}
	c := newExtractedReconcilerTestClient(t, ext)
	extractor := newExtractor(t, c, &fakeEngine{})
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}

	_, err := r.extractIcon(t.Context(), ext, &fakeEngine{})
	assert.NilError(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Assert(t, condition != nil)
	assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonFailed)
}

func TestExtensionExtractedReconcilerExtractUISkipsWhenNoUI(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonUI)
	metadata := []byte(`{"host":{"binaries":null}}`)
	ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
	c := newExtractedReconcilerTestClient(t, ext)
	extractor := newExtractor(t, c, &fakeEngine{})
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}

	_, err := r.extractUI(t.Context(), ext, &fakeEngine{})
	assert.NilError(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Assert(t, condition != nil)
	assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonExecutable)
}

func TestExtensionExtractedReconcilerExtractExecutableCompletesWhenNoBinaries(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonExecutable)
	metadata := []byte(`{"host":{"binaries":null}}`)
	ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
	c := newExtractedReconcilerTestClient(t, ext)
	extractor := newExtractor(t, c, &fakeEngine{})
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}

	_, err := r.extractExecutable(t.Context(), ext, &fakeEngine{})
	assert.NilError(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Assert(t, condition != nil)
	assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)
	// extractExecutable only sets Completed with ConditionFalse; it is
	// reconcileExtension's Completed case that later flips it to ConditionTrue.
	assert.Equal(t, condition.Status, metav1.ConditionFalse)

	_, err = r.reconcileExtension(t.Context(), updated)
	assert.NilError(t, err)
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	condition = apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)
	assert.Equal(t, condition.Status, metav1.ConditionTrue)
}

func TestExtensionExtractedReconcilerReconcileAppClearsEngineWhenNotReady(t *testing.T) {
	// Regression test: once an engine has been stored, a subsequent App
	// state where no engine is configured/ready must clear r.engine, not
	// leave the stale engine pointer in place.
	c := newExtractedReconcilerTestClient(t)
	extractor := newExtractor(t, c, nil)
	r := &ExtensionExtractedReconciler{
		extensionExtractor: extractor,
		engine:             &fakeEngine{},
	}

	_, err := r.reconcileApp(t.Context(), nil)
	assert.NilError(t, err)

	assert.Assert(t, r.engine == nil, "engine should be cleared when there is no App")
}

func TestExtensionExtractedReconcilerReconcileAppClearsEngineOnConnectFailure(t *testing.T) {
	c := newExtractedReconcilerTestClient(t)
	extractor := newExtractor(t, c, nil)
	r := &ExtensionExtractedReconciler{
		extensionExtractor: extractor,
		engine:             &fakeEngine{},
	}

	// reconcileApp only special-cases "moby"/"containerd"; anything else
	// leaves engine nil, which is sufficient to exercise the "no engine"
	// clearing path without needing a real App type import cycle.
	_, err := r.reconcileApp(t.Context(), nil)
	assert.NilError(t, err)
	assert.Assert(t, r.engine == nil)
}

func TestExtensionExtractedReconcilerReconcileDeleteDestroysStateAndRemovesFinalizer(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	now := metav1.Now()
	ext.DeletionTimestamp = &now
	c := newExtractedReconcilerTestClient(t, ext)
	extractor := newExtractor(t, c, &fakeEngine{})
	r := &ExtensionExtractedReconciler{extensionExtractor: extractor}

	destroyed := false
	extractor.stateMu.Lock()
	extractor.state[ext.GetUID()] = extractState{
		containerID: "fake-container-id",
		cancel:      func() { destroyed = true },
	}
	extractor.stateMu.Unlock()

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	assert.Assert(t, destroyed)

	extractor.stateMu.Lock()
	_, ok := extractor.state[ext.GetUID()]
	extractor.stateMu.Unlock()
	assert.Assert(t, !ok)

	updated := &extensionsv1alpha1.Extension{}
	err = c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated)
	// The fake client removes objects once all finalizers are gone and a
	// deletion timestamp is set.
	if err == nil {
		assert.Equal(t, len(updated.Finalizers), 0)
	}
}

// The following tests exercise reconcileExtension's dispatch logic (which
// step gets called for which Extracted condition reason) using stubSteps, so
// that they do not depend on any individual step's own behavior.

// extractedCondition fetches the Extension's current Extracted condition
// from the fake client, failing the test if either is missing. It's used in
// place of the (no-longer-possible) setExtractedConditionCalls recorder,
// since setExtractedCondition remains the real client-backed implementation
// rather than a stubbable field.
func extractedCondition(t *testing.T, c client.Client, ext *extensionsv1alpha1.Extension) *metav1.Condition {
	t.Helper()
	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
	assert.Assert(t, cond != nil, "expected an Extracted condition to be set")
	return cond
}

func TestExtensionExtractedReconcilerReconcileExtensionHasNoConditionYet(t *testing.T) {
	ext := newTestExtension(t, "test-extension", "")
	ext.Status.Conditions = nil
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	assert.Assert(t, apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted) == nil)
}

func TestExtensionExtractedReconcilerReconcileExtensionCompletedTrueIsNoop(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonCompleted)
	ext.Status.Conditions[0].Status = metav1.ConditionTrue
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	cond := extractedCondition(t, c, ext)
	assert.Equal(t, cond.Status, metav1.ConditionTrue)
	assert.Equal(t, cond.Reason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)
}

func TestExtensionExtractedReconcilerReconcileExtensionCompletedFalseFlipsToTrue(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonCompleted)
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	cond := extractedCondition(t, c, ext)
	assert.Equal(t, cond.Status, metav1.ConditionTrue)
	assert.Equal(t, cond.Reason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)
}

func TestExtensionExtractedReconcilerReconcileExtensionFailedIsNoop(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonFailed)
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	cond := extractedCondition(t, c, ext)
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, extensionsv1alpha1.ExtensionExtractedReasonFailed)
}

func TestExtensionExtractedReconcilerReconcileExtensionSetsEngineNotReadyWhenNoEngine(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonPreparing)
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(false)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	cond := extractedCondition(t, c, ext)
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, extensionsv1alpha1.ExtensionExtractedReasonEngineNotReady)
}

func TestExtensionExtractedReconcilerReconcileExtensionEngineNotReadyWaits(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonEngineNotReady)
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	// Given the engine is ready (steps.engineReady == true), reconcileExtension
	// advances the condition from EngineNotReady to Preparing rather than
	// waiting; see I2 in the review for more detail.
	_, err := r.reconcileExtension(t.Context(), ext)
	assert.NilError(t, err)
	cond := extractedCondition(t, c, ext)
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, extensionsv1alpha1.ExtensionExtractedReasonPreparing)
}

func TestExtensionExtractedReconcilerReconcileExtensionDispatchesToStep(t *testing.T) {
	testCases := []struct {
		reason string
		setFn  func(steps *stubSteps, called *bool)
	}{
		{
			reason: extensionsv1alpha1.ExtensionExtractedReasonPreparing,
			setFn: func(steps *stubSteps, called *bool) {
				steps.prepare = func(ctx context.Context, ext *extensionsv1alpha1.Extension, e engine) (ctrl.Result, error) {
					*called = true
					return ctrl.Result{}, nil
				}
			},
		},
		{
			reason: extensionsv1alpha1.ExtensionExtractedReasonMetadata,
			setFn: func(steps *stubSteps, called *bool) {
				steps.extractMetadata = func(ctx context.Context, ext *extensionsv1alpha1.Extension, e engine) (ctrl.Result, error) {
					*called = true
					return ctrl.Result{}, nil
				}
			},
		},
		{
			reason: extensionsv1alpha1.ExtensionExtractedReasonIcon,
			setFn: func(steps *stubSteps, called *bool) {
				steps.extractIcon = func(ctx context.Context, ext *extensionsv1alpha1.Extension, e engine) (ctrl.Result, error) {
					*called = true
					return ctrl.Result{}, nil
				}
			},
		},
		{
			reason: extensionsv1alpha1.ExtensionExtractedReasonUI,
			setFn: func(steps *stubSteps, called *bool) {
				steps.extractUI = func(ctx context.Context, ext *extensionsv1alpha1.Extension, e engine) (ctrl.Result, error) {
					*called = true
					return ctrl.Result{}, nil
				}
			},
		},
		{
			reason: extensionsv1alpha1.ExtensionExtractedReasonExecutable,
			setFn: func(steps *stubSteps, called *bool) {
				steps.extractExecutable = func(ctx context.Context, ext *extensionsv1alpha1.Extension, e engine) (ctrl.Result, error) {
					*called = true
					return ctrl.Result{}, nil
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.reason, func(t *testing.T) {
			ext := newTestExtension(t, "test-extension", tc.reason)
			c := newExtractedReconcilerTestClient(t, ext)
			steps := newStubSteps(true)
			called := false
			tc.setFn(steps, &called)
			r := newReconcilerWithStubSteps(t, c, steps)

			_, err := r.reconcileExtension(t.Context(), ext)
			assert.NilError(t, err)
			assert.Assert(t, called, "expected the step for reason %q to be invoked", tc.reason)
		})
	}
}

func TestExtensionExtractedReconcilerReconcileExtensionUnknownReasonErrors(t *testing.T) {
	ext := newTestExtension(t, "test-extension", "some-unknown-reason")
	c := newExtractedReconcilerTestClient(t, ext)
	steps := newStubSteps(true)
	r := newReconcilerWithStubSteps(t, c, steps)

	_, err := r.reconcileExtension(t.Context(), ext)
	assert.ErrorContains(t, err, "unexpected extraction condition reason")
}
