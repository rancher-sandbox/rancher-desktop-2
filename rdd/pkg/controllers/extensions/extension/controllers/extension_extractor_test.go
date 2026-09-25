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
	"runtime"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extensionsv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// The tests in this file exercise extensionExtractor's own methods (prepare,
// extractStep, etc.) in isolation via newExtractor, without going through
// ExtensionExtractedReconciler's dispatch logic. Shared test helpers (e.g.
// fakeEngine, newExtractor, newTestExtension, waitForExtractedReason) and
// TestMain live in extension_extracted_reconciler_test.go.

func TestExtensionExtractorPrepare(t *testing.T) {
	t.Run("sets EngineNotReady when engine missing", func(t *testing.T) {
		// Exercises extensionExtractor.prepare in isolation, without any
		// reconciler/App-watching machinery.
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonPreparing)
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, nil)

		_, err := r.prepare(t.Context(), ext, nil)
		assert.NilError(t, err)

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonEngineNotReady)
	})

	t.Run("stores container ID and advances", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonPreparing)
		c := newExtractedReconcilerTestClient(t, ext)
		fe := &fakeEngine{
			createForExportFn: func(ctx context.Context, ext *extensionsv1alpha1.Extension) (engineCreateForExportResult, error) {
				return engineCreateForExportResult{id: "new-container-id"}, nil
			},
		}
		r := newExtractor(t, c, fe)

		_, err := r.prepare(t.Context(), ext, fe)
		assert.NilError(t, err)

		r.stateMu.Lock()
		state := r.state[ext.GetUID()]
		r.stateMu.Unlock()
		assert.Equal(t, state.containerID, "new-container-id")

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	})

	t.Run("destroys existing state before recreating", func(t *testing.T) {
		// prepare must discard/destroy any pre-existing state for the
		// extension (e.g. left over from a previous, aborted attempt)
		// before creating a new container for export.
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonPreparing)
		c := newExtractedReconcilerTestClient(t, ext)
		fe := &fakeEngine{
			createForExportFn: func(ctx context.Context, ext *extensionsv1alpha1.Extension) (engineCreateForExportResult, error) {
				return engineCreateForExportResult{id: "new-container-id"}, nil
			},
		}
		r := newExtractor(t, c, fe)

		destroyed := false
		r.stateMu.Lock()
		r.state[ext.GetUID()] = extractState{
			containerID: "stale-container-id",
			cancel:      func() { destroyed = true },
		}
		r.stateMu.Unlock()

		_, err := r.prepare(t.Context(), ext, fe)
		assert.NilError(t, err)
		assert.Assert(t, destroyed, "stale state should have been destroyed")

		r.stateMu.Lock()
		state := r.state[ext.GetUID()]
		r.stateMu.Unlock()
		assert.Equal(t, state.containerID, "new-container-id")
	})

	t.Run("sets Preparing on create failure", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonPreparing)
		c := newExtractedReconcilerTestClient(t, ext)
		fe := &fakeEngine{
			createForExportFn: func(ctx context.Context, ext *extensionsv1alpha1.Extension) (engineCreateForExportResult, error) {
				return engineCreateForExportResult{}, fmt.Errorf("boom")
			},
		}
		r := newExtractor(t, c, fe)
		_, err := r.prepare(t.Context(), ext, fe)
		assert.NilError(t, err)

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonPreparing)
	})
}

func TestExtensionExtractorExtractStep(t *testing.T) {
	t.Run("waits for container", func(t *testing.T) {
		// When there is no containerID yet (prepare hasn't succeeded),
		// extractStep must report Preparing rather than attempting to
		// export.
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, &fakeEngine{})

		prepareCalled := false
		err := r.extractStep(t.Context(), ext, &fakeEngine{}, extractionStepMetadata, extensionsv1alpha1.ExtensionExtractedReasonIcon,
			func(ctx context.Context) (extractPrepareResult, error) {
				prepareCalled = true
				return extractPrepareResult{success: true}, nil
			}, nil)
		assert.NilError(t, err)
		assert.Assert(t, !prepareCalled, "prepare callback should not run without a containerID")

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonPreparing)
	})

	t.Run("runs prepare and exports entries", func(t *testing.T) {
		// Directly exercises extractStep (rather than one of the wrapper
		// methods like extractMetadata) to confirm it drives the prepare
		// callback, the export, and the nextReason transition on success.
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
		c := newExtractedReconcilerTestClient(t, ext)

		tempDir := t.TempDir()
		fe := &fakeEngine{
			exportFn: writeFileExportFn(t, map[string][]byte{"file.txt": []byte("hello")}),
		}
		r := newExtractor(t, c, fe)
		r.stateMu.Lock()
		r.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
		r.stateMu.Unlock()

		finalizeCalled := false
		err := r.extractStep(t.Context(), ext, fe, extractionStepMetadata, extensionsv1alpha1.ExtensionExtractedReasonIcon,
			func(ctx context.Context) (extractPrepareResult, error) {
				return extractPrepareResult{
					success: true,
					destDir: tempDir,
					entries: []extractEntry{{sourcePath: "file.txt"}},
				}, nil
			},
			func(ctx context.Context) error {
				finalizeCalled = true
				return nil
			})
		assert.NilError(t, err)

		waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonIcon)
		assert.Assert(t, finalizeCalled)

		data, err := os.ReadFile(filepath.Join(tempDir, "file.txt"))
		assert.NilError(t, err)
		assert.Equal(t, string(data), "hello")
	})

	t.Run("finalize failure sets Failed", func(t *testing.T) {
		// Regression test for the bug where a finalize failure (e.g. the
		// exported file cannot be opened/decoded) was silently overwritten
		// by the "completed successfully" transition to nextReason.
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
		c := newExtractedReconcilerTestClient(t, ext)

		fe := &fakeEngine{
			exportFn: writeFileExportFn(t, map[string][]byte{"file.txt": []byte("hello")}),
		}
		r := newExtractor(t, c, fe)
		r.stateMu.Lock()
		r.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
		r.stateMu.Unlock()

		err := r.extractStep(t.Context(), ext, fe, extractionStepMetadata, extensionsv1alpha1.ExtensionExtractedReasonIcon,
			func(ctx context.Context) (extractPrepareResult, error) {
				return extractPrepareResult{
					success: true,
					destDir: t.TempDir(),
					entries: []extractEntry{{sourcePath: "file.txt"}},
				}, nil
			},
			func(ctx context.Context) error {
				return fmt.Errorf("finalize boom")
			})
		assert.NilError(t, err)

		condition := waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonFailed)
		assert.Equal(t, condition.Status, metav1.ConditionFalse)

		// The condition must never advance to nextReason, which previously
		// happened because the finalize failure was masked.
		time.Sleep(50 * time.Millisecond)
		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		final := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Equal(t, final.Reason, extensionsv1alpha1.ExtensionExtractedReasonFailed)
	})

	t.Run("export failure sets Failed", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
		c := newExtractedReconcilerTestClient(t, ext)

		fe := &fakeEngine{
			exportFn: failingExportFn(fmt.Errorf("export boom")),
		}
		r := newExtractor(t, c, fe)
		r.stateMu.Lock()
		r.state[ext.GetUID()] = extractState{containerID: "fake-container-id"}
		r.stateMu.Unlock()

		err := r.extractStep(t.Context(), ext, fe, extractionStepMetadata, extensionsv1alpha1.ExtensionExtractedReasonIcon,
			func(ctx context.Context) (extractPrepareResult, error) {
				return extractPrepareResult{
					success: true,
					destDir: t.TempDir(),
					entries: []extractEntry{{sourcePath: "file.txt"}},
				}, nil
			}, nil)
		assert.NilError(t, err)

		condition := waitForExtractedReason(t, c, ext, extensionsv1alpha1.ExtensionExtractedReasonFailed)
		assert.Equal(t, condition.Status, metav1.ConditionFalse)
	})
}

// stubExtractStepCall records the currentStep/nextReason/prepare/finalize
// arguments passed to a stubbed extractStep, so a test can invoke prepare
// and finalize directly (each getting its own subtest) instead of running
// extractStep's own goroutine/state/engine scaffolding.
type stubExtractStepCall struct {
	currentStep extractionStep
	nextReason  string
	prepare     func(context.Context) (extractPrepareResult, error)
	finalize    func(context.Context) error
}

// captureExtractStep replaces r.extractStep with a stub that records its
// arguments into *call and returns nil, so the *Impl method under test can
// be exercised without extractStep's own logic running.
func captureExtractStep(r *extensionExtractor, call *stubExtractStepCall) {
	r.extractStep = func(
		ctx context.Context,
		ext *extensionsv1alpha1.Extension,
		e engine,
		currentStep extractionStep,
		nextReason string,
		prepare func(context.Context) (extractPrepareResult, error),
		finalize func(context.Context) error,
	) error {
		*call = stubExtractStepCall{
			currentStep: currentStep,
			nextReason:  nextReason,
			prepare:     prepare,
			finalize:    finalize,
		}
		return nil
	}
}

func TestExtensionExtractorExtractMetadataImpl(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonMetadata)
	c := newExtractedReconcilerTestClient(t, ext)
	r := newExtractor(t, c, &fakeEngine{})

	var call stubExtractStepCall
	captureExtractStep(r, &call)

	_, err := r.extractMetadataImpl(t.Context(), ext, &fakeEngine{})
	assert.NilError(t, err)
	assert.Equal(t, call.currentStep, extractionStepMetadata)
	assert.Equal(t, call.nextReason, extensionsv1alpha1.ExtensionExtractedReasonIcon)

	t.Run("prepare", func(t *testing.T) {
		result, err := call.prepare(t.Context())
		assert.NilError(t, err)
		assert.Assert(t, result.success)
		assert.Assert(t, result.destDir != "")
		assert.Assert(t, len(result.entries) == 1)
		assert.Equal(t, result.entries[0].sourcePath, "metadata.json")

		info, err := os.Stat(result.destDir)
		assert.NilError(t, err)
		assert.Assert(t, info.IsDir(), "install dir should have been created")
	})

	t.Run("finalize fails when metadata.json is missing", func(t *testing.T) {
		_, err := call.prepare(t.Context())
		assert.NilError(t, err)

		err = call.finalize(t.Context())
		assert.ErrorContains(t, err, "failed to open metadata file")
	})

	t.Run("finalize", func(t *testing.T) {
		result, err := call.prepare(t.Context())
		assert.NilError(t, err)

		metadata := []byte(`{"icon":"icon.png","host":{"binaries":null}}`)
		assert.NilError(t, os.WriteFile(filepath.Join(result.destDir, "metadata.json"), metadata, 0o644))

		assert.NilError(t, call.finalize(t.Context()))

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		assert.Assert(t, updated.Status.Metadata != nil)
		var manifest ExtensionManifest
		assert.NilError(t, json.Unmarshal(updated.Status.Metadata.Raw, &manifest))
		assert.Equal(t, manifest.Icon, "icon.png")
	})
}

func TestExtensionExtractorExtractIconImpl(t *testing.T) {
	ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonIcon)
	metadata := []byte(`{"icon":"icon.png","host":{"binaries":null}}`)
	ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
	c := newExtractedReconcilerTestClient(t, ext)
	r := newExtractor(t, c, &fakeEngine{})

	var call stubExtractStepCall
	captureExtractStep(r, &call)

	_, err := r.extractIconImpl(t.Context(), ext, &fakeEngine{})
	assert.NilError(t, err)
	assert.Equal(t, call.currentStep, extractionStepIcon)
	assert.Equal(t, call.nextReason, extensionsv1alpha1.ExtensionExtractedReasonUI)
	assert.Assert(t, call.finalize == nil, "extractIcon has no finalize step")

	t.Run("prepare", func(t *testing.T) {
		result, err := call.prepare(t.Context())
		assert.NilError(t, err)
		assert.Assert(t, result.success)
		assert.Assert(t, len(result.entries) == 1)
		assert.Equal(t, result.entries[0].sourcePath, "icon.png")
	})
}

func TestExtensionExtractorExtractUIImpl(t *testing.T) {
	t.Run("skips when no UI", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonUI)
		metadata := []byte(`{"host":{"binaries":null}}`)
		ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, &fakeEngine{})

		var call stubExtractStepCall
		captureExtractStep(r, &call)

		_, err := r.extractUIImpl(t.Context(), ext, &fakeEngine{})
		assert.NilError(t, err)
		assert.Assert(t, call.prepare == nil, "extractStep should not have been called")

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonExecutable)
	})

	t.Run("prepares export of the UI directory", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonUI)
		metadata := []byte(`{"host":{"binaries":null},"ui":{"dashboard-tab":{"root":"ui-dir"}}}`)
		ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, &fakeEngine{})

		var call stubExtractStepCall
		captureExtractStep(r, &call)

		_, err := r.extractUIImpl(t.Context(), ext, &fakeEngine{})
		assert.NilError(t, err)
		assert.Equal(t, call.currentStep, extractionStepUI)
		assert.Equal(t, call.nextReason, extensionsv1alpha1.ExtensionExtractedReasonExecutable)

		t.Run("prepare", func(t *testing.T) {
			result, err := call.prepare(t.Context())
			assert.NilError(t, err)
			assert.Assert(t, result.success)
			assert.Assert(t, len(result.entries) == 1)
			assert.Equal(t, result.entries[0].sourcePath, "ui-dir")
			assert.Assert(t, result.entries[0].isDirectory)
		})
	})
}

func TestExtensionExtractorExtractExecutableImpl(t *testing.T) {
	t.Run("completes when no binaries", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonExecutable)
		metadata := []byte(`{"host":{"binaries":null}}`)
		ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, &fakeEngine{})

		var call stubExtractStepCall
		captureExtractStep(r, &call)

		_, err := r.extractExecutableImpl(t.Context(), ext, &fakeEngine{})
		assert.NilError(t, err)
		assert.Assert(t, call.prepare == nil, "extractStep should not have been called")

		updated := &extensionsv1alpha1.Extension{}
		assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionExtracted)
		assert.Assert(t, condition != nil)
		assert.Equal(t, condition.Reason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)
	})

	t.Run("prepares export of the binaries", func(t *testing.T) {
		ext := newTestExtension(t, "test-extension", extensionsv1alpha1.ExtensionExtractedReasonExecutable)
		metadata := []byte(fmt.Sprintf(`{"host":{"binaries":[{"%s":[{"path":"bin/tool"}]}]}}`, runtime.GOOS))
		ext.Status.Metadata = &apiextensionsv1.JSON{Raw: metadata}
		c := newExtractedReconcilerTestClient(t, ext)
		r := newExtractor(t, c, &fakeEngine{})

		var call stubExtractStepCall
		captureExtractStep(r, &call)

		_, err := r.extractExecutableImpl(t.Context(), ext, &fakeEngine{})
		assert.NilError(t, err)
		assert.Equal(t, call.currentStep, extractionStepExecutable)
		assert.Equal(t, call.nextReason, extensionsv1alpha1.ExtensionExtractedReasonCompleted)

		t.Run("prepare", func(t *testing.T) {
			result, err := call.prepare(t.Context())
			assert.NilError(t, err)
			assert.Assert(t, result.success)
			assert.Assert(t, len(result.entries) == 1)
			assert.Equal(t, result.entries[0].sourcePath, "bin/tool")
		})
	})
}
