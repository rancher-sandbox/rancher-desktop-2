// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/atomicmap"
)

// extractionStep is the descriptive name of each step, used in status messages.
type extractionStep string

const (
	extractionStepPreparing  extractionStep = "preparing"
	extractionStepMetadata   extractionStep = "metadata"
	extractionStepIcon       extractionStep = "icon"
	extractionStepUI         extractionStep = "UI"
	extractionStepExecutable extractionStep = "executable"
	extractionStepContainer  extractionStep = "container"
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
	events.EventRecorder
	// ctx is the context that lasts for the lifetime of the reconciler; used
	// for processes which may outlive any individual reconcile.
	ctx context.Context

	// state holds the current extraction state.
	state atomicmap.AtomicMap[types.UID, extractState]

	prepare           func(ctx context.Context, ext *v1alpha1.Extension, e engine) error
	extractMetadata   func(ctx context.Context, ext *v1alpha1.Extension, e engine) error
	extractIcon       func(ctx context.Context, ext *v1alpha1.Extension, e engine) error
	extractUI         func(ctx context.Context, ext *v1alpha1.Extension, e engine) error
	extractExecutable func(ctx context.Context, ext *v1alpha1.Extension, e engine) error
	extractContainer  func(ctx context.Context, ext *v1alpha1.Extension, e engine) error

	// extractStep implements the scaffolding for one step of the extraction
	// process.  [currentStep] represents the current step of the extraction
	// process; this is used for managing the state, as well as to be displayed in
	// the status messages.  [nextReason] is the reason to set for the next
	// extraction step once this one completes.  The given [prepare] function is
	// used to prepare the export options for the current extraction step. If any
	// error occurs, it is expected to have set the status condition before
	// returning.  [finalize] is the function to call to finalize the extraction
	// step, and is only called after all of the files have been extracted; it may
	// be nil if no finalization is needed.
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

func (r *extensionExtractor) prepareImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
	// Check to make sure we don't have any duplicate state for this extension.
	alreadyRunning := false
	state := r.state.Modify(ext.GetUID(), func(value extractState, exists bool) (extractState, bool) {
		if exists && value.step == extractionStepPreparing {
			alreadyRunning = true
			return value, true
		}
		return value, false
	})
	if alreadyRunning {
		return nil
	}
	state.destroy()

	if e == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonEngineNotReady, "The container engine is not ready")
	}
	// Create a container for export; make sure to use the controller context.
	result, err := e.createForExport(r.ctx, ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, fmt.Sprintf("Failed to create container for export: %v", err))
	}

	// Store the new state for this extension.
	r.state.Store(ext.GetUID(), extractState{
		containerID: result.id,
		cleanup:     result.cleanup,
	})

	return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
		v1alpha1.ExtensionExtractedReasonMetadata, "Metadata extraction in progress")
}

func (r *extensionExtractor) extractMetadataImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
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
				var buf v1.JSON
				var err error
				buf.Raw, err = os.ReadFile(filepath.Join(dir, "metadata.json"))
				if err != nil {
					return fmt.Errorf("failed to read metadata file: %w", err)
				}
				latest.Status.Metadata = &buf
				var manifest ExtensionManifest
				if err := json.Unmarshal(buf.Raw, &manifest); err != nil {
					return err
				}
				if ui, ok := manifest.UI["dashboard-tab"]; ok {
					latest.Status.UI = &v1alpha1.ExtensionUIStatus{
						DashboardTab: &v1alpha1.ExtensionDashboardTabStatus{
							Title:  ui.Title,
							Src:    ui.Src,
							Socket: manifest.VM.Exposes.Socket != "" || ui.Backend.Socket != "",
						},
					}
				}
				return r.Status().Update(ctx, &latest)
			})
		})
	return err
}

func (r *extensionExtractor) extractIconImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
	if state, _ := r.state.Load(ext.GetUID()); state.step == extractionStepIcon {
		return nil
	}
	manifest, err := getManifest(ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			err.Error())
	}
	if manifest == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			"Extension metadata is not available")
	}
	if manifest.Icon == "" {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonUI,
			"Extension manifest does not specify an icon; skipping icon extraction")
	}
	return r.extractStep(ctx, ext, e, extractionStepIcon, v1alpha1.ExtensionExtractedReasonUI,
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
}

func (r *extensionExtractor) extractUIImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
	if state, _ := r.state.Load(ext.GetUID()); state.step == extractionStepUI {
		return nil
	}
	manifest, err := getManifest(ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			err.Error())
	}
	if manifest == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			"Extension metadata is not available")
	}
	ui, ok := manifest.UI["dashboard-tab"]
	if !ok {
		// No UI; skip to the next state.
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonExecutable,
			"Extension does not have UI")
	}
	return r.extractStep(ctx, ext, e, extractionStepUI, v1alpha1.ExtensionExtractedReasonExecutable,
		func(ctx context.Context) (extractPrepareResult, error) {
			dir, err := extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			// The extension install directory was created while extracting metadata.
			dir = filepath.Join(dir, "ui")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to create UI directory: %v", err))
			}
			return extractPrepareResult{
				success: true,
				destDir: dir,
				entries: []extractEntry{
					{
						sourcePath:  ui.Root,
						isDirectory: true,
					},
				},
			}, nil
		}, nil)
}

func (r *extensionExtractor) extractExecutableImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
	if state, _ := r.state.Load(ext.GetUID()); state.step == extractionStepExecutable {
		return nil
	}
	r.Eventf(ext, nil, corev1.EventTypeNormal, "ExtractBinaries", "Reconciling",
		"Extracting executables for extension")
	manifest, err := getManifest(ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			err.Error())
	}
	if manifest == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			"Extension metadata is not available")
	}
	var binaries []string
	for _, binary := range manifest.Host.Binaries {
		for _, platformBinary := range binary.Get() {
			binaries = append(binaries, platformBinary.Path)
		}
	}
	if len(binaries) < 1 {
		r.Eventf(ext, nil, corev1.EventTypeNormal, "ExtractNoBinaries", "ExtractContainer",
			"Extension does not have any binaries for the current OS")
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonContainer,
			"Extension does not have any binaries for the current OS")
	}
	return r.extractStep(ctx, ext, e, extractionStepExecutable, v1alpha1.ExtensionExtractedReasonContainer,
		func(ctx context.Context) (extractPrepareResult, error) {
			dir, err := extensionInstallDir(ext)
			if err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to determine extension install directory: %v", err))
			}
			dir = filepath.Join(dir, "bin")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return extractPrepareResult{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
					v1alpha1.ExtensionExtractedReasonFailed,
					fmt.Sprintf("Failed to create bin directory: %v", err))
			}
			result := extractPrepareResult{
				success: true,
				destDir: dir,
			}
			for _, binary := range binaries {
				result.entries = append(result.entries, extractEntry{sourcePath: binary})
			}
			return result, nil
		}, nil)
}

func (r *extensionExtractor) extractContainerImpl(ctx context.Context, ext *v1alpha1.Extension, e engine) error {
	if state, _ := r.state.Load(ext.GetUID()); state.step == extractionStepContainer {
		return nil
	}
	manifest, err := getManifest(ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			err.Error())
	}
	if manifest == nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			"Extension metadata is not available")
	}
	if manifest.VM.ComposeFile == "" && manifest.VM.Image == "" {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFinishing,
			"Extension does not have any container configuration")
	}
	if manifest.VM.ComposeFile != "" && manifest.VM.Image != "" {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			"Extension cannot have both Compose file and Image configured")
	}
	dir, err := extensionInstallDir(ext)
	if err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			fmt.Sprintf("Failed to determine extension install directory: %v", err))
	}
	dir = filepath.Join(dir, "compose")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFailed,
			fmt.Sprintf("Failed to create compose directory: %v", err))
	}
	if manifest.VM.Image != "" {
		r.Eventf(ext, nil, corev1.EventTypeNormal, "ExtractContainer", "GenerateComposeFile",
			"Generating Compose file for image: %q", manifest.VM.Image)
		file, err := os.Create(filepath.Join(dir, "compose.yaml"))
		if err != nil {
			return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
				v1alpha1.ExtensionExtractedReasonFailed,
				fmt.Sprintf("Failed to create Compose file: %v", err))
		}
		defer file.Close()
		composeData := map[string]any{
			"services": map[string]any{
				"web": map[string]any{
					"image": manifest.VM.Image,
				},
			},
		}
		composeBytes, err := yaml.Marshal(composeData)
		if err != nil {
			return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
				v1alpha1.ExtensionExtractedReasonFailed,
				fmt.Sprintf("Failed to marshal Compose data: %v", err))
		}
		_, err = file.Write(composeBytes)
		if err != nil {
			return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
				v1alpha1.ExtensionExtractedReasonFailed,
				fmt.Sprintf("Failed to write to Compose file: %v", err))
		}
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonFinishing,
			fmt.Sprintf("Wrote Compose file for image: %q", manifest.VM.Image))
	}
	r.Eventf(ext, nil, corev1.EventTypeNormal, "ExtractContainer", "ExtractComposeData",
		"Extracting Compose data from extension from %q to %q",
		manifest.VM.ComposeFile, dir)
	return r.extractStep(ctx, ext, e, extractionStepContainer, v1alpha1.ExtensionExtractedReasonFinishing,
		func(context.Context) (extractPrepareResult, error) {
			result := extractPrepareResult{
				success: true,
				destDir: dir,
				entries: []extractEntry{
					{
						sourcePath:  path.Dir(manifest.VM.ComposeFile),
						isDirectory: true,
					},
				},
			}
			return result, nil
		}, func(context.Context) error {
			if path.Base(manifest.VM.ComposeFile) != "compose.yaml" {
				out, err := os.Create(filepath.Join(dir, "compose.yaml"))
				if err != nil {
					return fmt.Errorf("failed to create compose file: %w", err)
				}
				defer out.Close()
				in, err := os.Open(filepath.Join(dir, filepath.Base(manifest.VM.ComposeFile)))
				if err != nil {
					return fmt.Errorf("failed to open original compose file: %w", err)
				}
				defer in.Close()
				if _, err := io.Copy(out, in); err != nil {
					return fmt.Errorf("failed to copy compose file: %w", err)
				}
			}
			return nil
		})
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
	log := logf.FromContext(ctx)
	state, _ := r.state.Load(ext.GetUID())

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
		if _, ok := errors.AsType[requeueError](err); ok {
			log.Error(err, "Requeue not supported during prepare step")
		} else {
			return err
		}
	}
	if !prepareResult.success {
		return nil
	}

	extractCtx, extractCancel := context.WithCancel(r.ctx)

	state.step = currentStep
	state.cancel = extractCancel
	r.state.Store(ext.GetUID(), state)

	go func() {
		defer extractCancel()
		total := len(prepareResult.entries)
		for i, entry := range prepareResult.entries {
			if extractCtx.Err() != nil {
				return
			}
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
				case res, ok := <-ch:
					switch {
					case !ok:
						return
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
		if extractCtx.Err() != nil {
			return
		}
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

	return nil
}

func (r *extensionExtractor) abort(uid types.UID) {
	if state, ok := r.state.LoadAndDelete(uid); ok {
		state.destroy()
	}
}

// setExtractedCondition sets the extracted condition for the given extension
// with the specified status, reason, and message.  If the reason is failed, it
// also cancels any ongoing extraction process.
func (r *extensionExtractor) setExtractedCondition(ctx context.Context, ext *v1alpha1.Extension, status metav1.ConditionStatus, reason, message string) error {
	if reason == v1alpha1.ExtensionExtractedReasonFailed {
		if state, ok := r.state.LoadAndDelete(ext.GetUID()); ok {
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
		if err := r.Status().Update(ctx, latest); err != nil {
			return err
		}
		return requeueError(0)
	})
}
