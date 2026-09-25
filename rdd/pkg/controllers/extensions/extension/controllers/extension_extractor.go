package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// extractionStep is the descriptive name of each step, used in status messages.
type extractionStep string

const (
	extractionStepPreparing  extractionStep = "preparing"
	extractionStepMetadata   extractionStep = "metadata"
	extractionStepIcon       extractionStep = "icon"
	extractionStepUI         extractionStep = "UI"
	extractionStepExecutable extractionStep = "executable"
	extractionStepCompleted  extractionStep = "completed"
)

type extractState struct {
	// containerID is the ID of the container created for the export operation.
	containerID string
	// cancel the currently in-progress operation, if any.
	cancel context.CancelFunc
	// cleanup the resources associated with the extraction, if any.  This should
	// be called after cancel has been invoked.
	cleanup context.CancelFunc
	step    extractionStep
}

// Destroy releases any resources related to this extraction.
func (s *extractState) destroy() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.cleanup != nil {
		s.cleanup()
	}
}

// extensionExtractor handles the actual extraction progress.  Each step of the
// extraction (extractMetadata, etc.) is a function field so that it can be
// replaced during tests; on creation, they should be set to the *Impl methods.
type extensionExtractor struct {
	client.Client
	// ctx is the context that lasts for the lifetime of the reconciler; used
	// for processes which may outlive any individual reconcile.
	ctx context.Context

	// state holds the current extraction state and is protected by stateMu.
	state   map[types.UID]extractState
	stateMu sync.Mutex

	prepare           func(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error)
	extractMetadata   func(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error)
	extractIcon       func(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error)
	extractUI         func(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error)
	extractExecutable func(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error)
	reconcileDelete   func(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error)

	// extractStep implements the scaffolding for one step of the extraction process.
	// [currentStep] represents the current step of the extraction process; this is
	// used for managing the state, as well as to be displayed in the status
	// messages.  [nextReason] is the reason to set for the next extraction step
	// once this one completes.  The given [prepare] function is used to prepare the
	// export options for the current extraction step. If any error occurs, it is
	// expected to have set the status condition before returning.  [finalize] is
	// the function to call to finalize the extraction step, and is called after all
	// of the files have been extracted; it may be nil if no finalization is needed.
	extractStep func(
		ctx context.Context,
		ext *v1alpha1.Extension,
		e engine,
		currentStep extractionStep,
		nextReason string,
		prepare func(context.Context) (extractPrepareResult, error),
		finalize func(context.Context) error,
	) error
}

// extractPrepareResult is returned from the prepare function passed in to
// [extensionExtractor.extractStep].
type extractPrepareResult struct {
	// If false, abort the extraction
	success bool
	// The directory in which to write the results.
	destDir string
	// The list of entries to extract.
	entries []extractEntry
}

// extractEntry represents a single file or directory to be extracted.
type extractEntry struct {
	// sourcePath is the path within the archive to extract; it must be relative.
	sourcePath string
	// isDirectory indicates whether the entry is a directory.  If the actual
	// entry differs, an error is returned and the extraction is aborted.
	isDirectory bool
}

func (r *extensionExtractor) prepareImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error) {
	// Check to make sure we don't have any duplicate state for this extension.
	r.stateMu.Lock()
	state := r.state[ext.GetUID()]
	delete(r.state, ext.GetUID())
	r.stateMu.Unlock()
	state.destroy()

	if e == nil {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonEngineNotReady, "The container engine is not ready")
	}
	// Create a container for export; make sure to use the controller context.
	result, err := e.createForExport(r.ctx, ext)
	if err != nil {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, fmt.Sprintf("Failed to create container for export: %v", err))
	}

	// Store the new state for this extension.
	r.stateMu.Lock()
	r.state[ext.GetUID()] = extractState{
		containerID: result.id,
		cleanup:     result.cleanup,
	}
	r.stateMu.Unlock()

	return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
		v1alpha1.ExtensionExtractedReasonMetadata, "Metadata extraction in progress")
}

func (r *extensionExtractor) extractMetadataImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error) {
	var dir string
	err := r.extractStep(ctx, ext, e, extractionStepMetadata, v1alpha1.ExtensionExtractedReasonIcon,
		func(ctx context.Context) (extractPrepareResult, error) {
			var err error
			dir, err = extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to create extension install directory: %v", err))
			}
			return extractPrepareResult{
				success: true,
				destDir: dir,
				entries: []extractEntry{
					{sourcePath: "metadata.json"},
				},
			}, nil
		},
		func(ctx context.Context) error {
			return retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var latest v1alpha1.Extension
				if err := r.Get(ctx, client.ObjectKeyFromObject(ext), &latest); err != nil {
					return err
				}
				file, err := os.Open(filepath.Join(dir, "metadata.json"))
				if err != nil {
					return fmt.Errorf("failed to open metadata file: %w", err)
				}
				defer file.Close()
				if err := json.NewDecoder(file).Decode(&latest.Status.Metadata); err != nil {
					return err
				}
				return r.Status().Update(ctx, &latest)
			})
		})
	return ctrl.Result{}, err
}

func (r *extensionExtractor) extractIconImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error) {
	var manifest ExtensionManifest
	if err := json.Unmarshal(ext.Status.Metadata.Raw, &manifest); err != nil {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			fmt.Sprintf("Failed to unmarshal extension manifest: %v", err))
	}
	err := r.extractStep(ctx, ext, e, extractionStepIcon, v1alpha1.ExtensionExtractedReasonUI,
		func(ctx context.Context) (extractPrepareResult, error) {
			dir, err := extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			return extractPrepareResult{
				success: true,
				destDir: dir,
				entries: []extractEntry{
					{sourcePath: manifest.Icon},
				},
			}, nil
		}, nil)
	return ctrl.Result{}, err
}

func (r *extensionExtractor) extractUIImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error) {
	var manifest ExtensionManifest
	if err := json.Unmarshal(ext.Status.Metadata.Raw, &manifest); err != nil {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			fmt.Sprintf("Failed to unmarshal extension manifest: %v", err))
	}
	ui, ok := manifest.UI["dashboard-tab"]
	if !ok {
		// No UI; skip to the next state.
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonExecutable,
			"Extension does not have UI")
	}
	err := r.extractStep(ctx, ext, e, extractionStepUI, v1alpha1.ExtensionExtractedReasonExecutable,
		func(ctx context.Context) (extractPrepareResult, error) {
			dir, err := extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			// The directory should be extracted in the previous step.
			return extractPrepareResult{
				success: true,
				destDir: filepath.Join(dir, "ui"),
				entries: []extractEntry{
					{
						sourcePath:  ui.Root,
						isDirectory: true,
					},
				},
			}, nil
		}, nil)
	return ctrl.Result{}, err
}

func (r *extensionExtractor) extractExecutableImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) (ctrl.Result, error) {
	var manifest ExtensionManifest
	if err := json.Unmarshal(ext.Status.Metadata.Raw, &manifest); err != nil {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			fmt.Sprintf("Failed to unmarshal extension manifest: %v", err))
	}
	var binaries []string
	for _, binary := range manifest.Host.Binaries {
		for _, platformBinary := range binary.Get() {
			binaries = append(binaries, platformBinary.Path)
		}
	}
	if len(binaries) < 1 {
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonCompleted,
			"Extension does not have any binaries for the current OS")
	}
	err := r.extractStep(ctx, ext, e, extractionStepExecutable, v1alpha1.ExtensionExtractedReasonCompleted,
		func(ctx context.Context) (extractPrepareResult, error) {
			dir, err := extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			result := extractPrepareResult{
				success: true,
				destDir: filepath.Join(dir, "bin"),
			}
			for _, binary := range binaries {
				result.entries = append(result.entries, extractEntry{sourcePath: binary})
			}
			return result, nil
		}, nil)
	return ctrl.Result{}, err
}

func (r *extensionExtractor) extractStepImpl(
	ctx context.Context,
	ext *v1alpha1.Extension,
	e engine,
	currentStep extractionStep,
	nextReason string,
	prepare func(context.Context) (extractPrepareResult, error),
	finalize func(context.Context) error,
) error {
	r.stateMu.Lock()
	state := r.state[ext.GetUID()]
	r.stateMu.Unlock()

	if state.containerID == "" {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, "Container for export is not ready")
	}

	if state.step == currentStep {
		// We are already extracting; don't run again in parallel.
		return nil
	}

	condition := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionExtracted)
	if condition == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, "The extracted condition is not set")
	}

	if e == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonEngineNotReady, "The container engine is not ready")
	}

	prepareResult, err := prepare(ctx)
	if err != nil {
		return nil
	}
	if !prepareResult.success {
		return nil
	}

	extractCtx, extractCancel := context.WithCancel(r.ctx)

	go func() {
		defer extractCancel()
		total := len(prepareResult.entries)
		for i, entry := range prepareResult.entries {
			ch := make(chan result[engineExportProgress], 1)
			err = e.export(extractCtx, engineExportOptions{
				id:          state.containerID,
				destDir:     prepareResult.destDir,
				sourcePath:  entry.sourcePath,
				isDirectory: entry.isDirectory,
				ch:          ch,
			})
			if err != nil {
				_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to export %s: %v", currentStep, err))
				return
			}
		selectLoop:
			for {
				select {
				case res := <-ch:
					switch {
					case res.err != nil:
						_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
							v1alpha1.ExtensionExtractedReasonFailed,
							fmt.Sprintf("Failed to export %s: %v", currentStep, res.err))
						return
					case res.value == engineExportProgressStarted:
						_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
							condition.Reason, fmt.Sprintf("%s extraction started (%d/%d)", currentStep, i+1, total))
					case res.value == engineExportProgressContainerCreated:
						_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
							condition.Reason, fmt.Sprintf("%s extraction prepared (%d/%d)", currentStep, i+1, total))
					case res.value == engineExportProgressCopying:
						_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
							condition.Reason, fmt.Sprintf("%s extraction in progress (%d/%d)", currentStep, i+1, total))
					case res.value == engineExportProgressCompleted:
						_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
							condition.Reason, fmt.Sprintf("%s extraction completed (%d/%d)", currentStep, i+1, total))
						break selectLoop
					}
				case <-extractCtx.Done():
					return
				}
			}
		}
		// All entries have been extracted correctly
		if finalize != nil {
			if err := finalize(extractCtx); err != nil {
				_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to finalize %s extraction: %v", currentStep, err))
				return
			}
		}
		_ = r.setExtractedCondition(extractCtx, ext, metav1.ConditionFalse,
			nextReason, fmt.Sprintf("%s extraction completed successfully", currentStep))
	}()

	state.step = currentStep
	state.cancel = extractCancel
	r.stateMu.Lock()
	r.state[ext.GetUID()] = state
	r.stateMu.Unlock()

	return nil
}

func (r *extensionExtractor) reconcileDeleteImpl(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	r.stateMu.Lock()
	state := r.state[ext.GetUID()]
	delete(r.state, ext.GetUID())
	r.stateMu.Unlock()
	state.destroy()

	return ctrl.Result{}, base.RemoveFinalizerWithRetry(ctx, r.Client, ext, extractedFinalizer)
}

// setExtractedCondition sets the extracted condition for the given extension
// with the specified status, reason, and message.  If the reason is failed, it
// also cancels any ongoing extraction process.
func (r *extensionExtractor) setExtractedCondition(ctx context.Context, ext *v1alpha1.Extension, status metav1.ConditionStatus, reason, message string) error {
	if reason == v1alpha1.ExtensionExtractedReasonFailed {
		r.stateMu.Lock()
		state, ok := r.state[ext.GetUID()]
		delete(r.state, ext.GetUID())
		r.stateMu.Unlock()
		if ok {
			state.destroy()
		}
	}
	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ExtensionConditionExtracted,
			Status:             status,
			ObservedGeneration: latest.Generation,
			Reason:             reason,
			Message:            message,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	})
}
