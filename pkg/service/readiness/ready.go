// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

// Package readiness waits for the embedded control plane to become operational
// by polling the API server's /readyz endpoint and checking that expected CRDs
// are established and webhook configurations are created.
package readiness

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// WaitForReady waits for the control plane to be ready.
func WaitForReady(ctx context.Context, config *rest.Config, logging bool) error {
	logger := klog.FromContext(ctx)
	if !logging {
		logger = logr.Discard()
	}
	logger.Info("Waiting for /readyz to succeed")
	lastSeenUnready := sets.New[string]()

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return errors.New("context canceled")
		default:
		}

		res := client.RESTClient().Get().AbsPath("/readyz").Do(ctx)
		if _, err := res.Raw(); err != nil {
			unreadyComponents := unreadyComponentsFromError(err)
			if !lastSeenUnready.Equal(unreadyComponents) {
				klog.ErrorS(err, "Control plane not ready", "unreadyComponents", sets.List[string](unreadyComponents))
				lastSeenUnready = unreadyComponents
			}
		}

		// When there is an error for invalid certificate, we should exit immediately as there is no point in retrying.
		if res.Error() != nil {
			if isCertificateError(res.Error()) {
				logger.Error(res.Error(), "control plane not ready")
				logger.Info("This is likely due to certificates folder containing invalid certificates. Please fix them and restart the control plane.")
				return res.Error()
			}
		}

		var rc int
		res.StatusCode(&rc)
		if rc == http.StatusOK {
			logger.Info("Control plane is ready")
			break
		}
		klog.V(1).InfoS("Control plane not ready", "status", rc, "unreadyComponents", sets.List[string](lastSeenUnready))

		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// WaitForReadyWithCRDs waits for the control plane to be ready and all expected CRDs to be established.
func WaitForReadyWithCRDs(ctx context.Context, config *rest.Config, expectedControllers []base.Controller, logging bool) error {
	logger := klog.FromContext(ctx)
	if !logging {
		logger = logr.Discard()
	}

	// First wait for basic readiness
	logger.Info("Waiting for basic control plane readiness")
	if err := WaitForReady(ctx, config, logging); err != nil {
		return err
	}
	logger.Info("Control plane ready, now checking CRDs", "controllers", len(expectedControllers))

	// Then wait for CRDs to be established
	if err := waitForCRDsEstablished(ctx, config, expectedControllers, logging); err != nil {
		logger.Error(err, "CRD establishment failed")
		return err
	}
	logger.Info("CRDs established, now checking webhook configurations")

	// Finally wait for webhook configurations to be created
	if err := waitForWebhookConfigurations(ctx, config, expectedControllers, logging); err != nil {
		logger.Error(err, "Webhook configuration check failed")
		return err
	}
	logger.Info("All readiness checks completed successfully")
	return nil
}

// waitForCRDsEstablished waits for all CRDs from the given controllers to be established.
func waitForCRDsEstablished(ctx context.Context, config *rest.Config, controllers []base.Controller, logging bool) error {
	logger := klog.FromContext(ctx)
	if !logging {
		logger = logr.Discard()
	}

	if len(controllers) == 0 {
		logger.Info("No controllers specified, skipping CRD readiness check")
		return nil
	}

	logger.Info("Waiting for CRDs to be established", "controllers", len(controllers))

	apiextensionsClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	// Extract CRD names from controllers
	var expectedCRDNames []string
	for _, controller := range controllers {
		crdData := controller.GetCRDData()
		if crdData == "" {
			continue // Skip controllers without CRDs
		}

		decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(crdData))
		for {
			var crd apiextensionsv1.CustomResourceDefinition
			if err := decoder.Decode(&crd); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return fmt.Errorf("failed to unmarshal CRD for controller %s: %w", controller.GetName(), err)
			}
			if crd.Name != "" {
				expectedCRDNames = append(expectedCRDNames, crd.Name)
			}
		}
	}

	if len(expectedCRDNames) == 0 {
		logger.Info("No CRDs found in controllers, skipping CRD readiness check")
		return nil
	}

	logger.Info("Checking CRD establishment", "expectedCRDs", expectedCRDNames)

	crdClient := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions()

	expectedCRDs := sets.New(expectedCRDNames...)
	establishedCRDs := sets.New[string]()

	// First, check if any CRDs are already established
	var initialCRDs *apiextensionsv1.CustomResourceDefinitionList
	initialCRDs, err = crdClient.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list CRDs: %w", err)
	}

	for _, crd := range initialCRDs.Items {
		if expectedCRDs.Has(crd.Name) && isCRDEstablished(&crd) {
			establishedCRDs.Insert(crd.Name)
			logger.V(1).Info("CRD already established", "crd", crd.Name)
		}
	}

	// Check if all are already established
	if establishedCRDs.IsSuperset(expectedCRDs) {
		logger.Info("All CRDs are already established")
		return nil
	}

	// Watch for CRD changes
	const timeout = time.Minute
	watchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := crdClient.Watch(watchCtx, metav1.ListOptions{ResourceVersion: initialCRDs.ResourceVersion})
	if err != nil {
		return fmt.Errorf("failed to start CRD watch: %w", err)
	}
	defer watcher.Stop()

	klog.InfoS("Starting CRD watch", "timeout", timeout, "expected", expectedCRDNames)

	for {
		select {
		case <-watchCtx.Done():
			established := sets.List(establishedCRDs)
			missing := expectedCRDs.Difference(establishedCRDs)
			klog.ErrorS(watchCtx.Err(), "CRD establishment timeout", "established", established, "missing", sets.List(missing), "expected", expectedCRDNames)
			return errors.New("timeout waiting for CRDs to be established")
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return errors.New("watch channel closed unexpectedly")
			}

			crd, ok := event.Object.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				continue
			}

			// Only care about CRDs we're waiting for
			if !expectedCRDs.Has(crd.Name) {
				continue
			}

			if event.Type == watch.Added || event.Type == watch.Modified {
				if !establishedCRDs.Has(crd.Name) && isCRDEstablished(crd) {
					logger.Info("CRD became established", "crd", crd.Name)
					establishedCRDs.Insert(crd.Name)

					if establishedCRDs.IsSuperset(expectedCRDs) {
						logger.Info("All CRDs are established")
						return nil
					}
				}
			}
		}
	}
}

// isCRDEstablished checks if a CRD has the Established condition set to true.
func isCRDEstablished(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
			return true
		}
	}
	return false
}

// waitForWebhookConfigurations waits for all webhook configurations from the given controllers to be created.
func waitForWebhookConfigurations(ctx context.Context, config *rest.Config, controllers []base.Controller, logging bool) error {
	logger := klog.FromContext(ctx)
	if !logging {
		logger = logr.Discard()
	}

	if len(controllers) == 0 {
		logger.Info("No controllers specified, skipping webhook configuration readiness check")
		return nil
	}

	logger.Info("Waiting for webhook configurations", "controllers", len(controllers))

	// Check which controllers need webhook configurations
	type webhookInfo struct {
		name        string
		webhookType base.WebhookType
	}
	var expectedWebhooks []webhookInfo
	for _, controller := range controllers {
		// Check if controller implements WebhookController interface
		webhookController, ok := controller.(base.WebhookController)
		if !ok {
			continue
		}
		controllerName := controller.GetName()
		webhookManagers := webhookController.GetWebhookManagers()
		for _, mgr := range webhookManagers {
			if mgr == nil {
				continue
			}
			webhookName := mgr.GetConfigName()
			webhookType := mgr.GetWebhookType()
			expectedWebhooks = append(expectedWebhooks, webhookInfo{
				name:        webhookName,
				webhookType: webhookType,
			})
			logger.Info("Controller has webhook configuration", "controller", controllerName, "webhook", webhookName, "type", webhookType)
		}
	}
	if len(expectedWebhooks) == 0 {
		logger.Info("No webhook configurations expected, skipping webhook readiness check")
		return nil
	}
	expectedWebhookSet := sets.New[string]()
	for _, wh := range expectedWebhooks {
		expectedWebhookSet.Insert(wh.name)
	}
	logger.Info("Waiting for webhook configurations to be created", "webhooks", sets.List(expectedWebhookSet))

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	validatingClient := client.AdmissionregistrationV1().ValidatingWebhookConfigurations()
	mutatingClient := client.AdmissionregistrationV1().MutatingWebhookConfigurations()

	foundWebhooks := sets.New[string]()

	// First, check if any webhooks are already created
	for _, webhook := range expectedWebhooks {
		var err error
		if webhook.webhookType == base.MutatingWebhook {
			_, err = mutatingClient.Get(ctx, webhook.name, metav1.GetOptions{})
		} else {
			_, err = validatingClient.Get(ctx, webhook.name, metav1.GetOptions{})
		}
		if err == nil {
			foundWebhooks.Insert(webhook.name)
			logger.V(1).Info("Webhook configuration already exists", "webhook", webhook.name, "type", webhook.webhookType)
		}
	}

	// Check if all are already created
	allFound := foundWebhooks.IsSuperset(expectedWebhookSet)

	if allFound {
		logger.Info("All webhook configurations already exist")
		return nil
	}

	// Wait for webhook configurations to be created (they are created asynchronously)
	// Use a reasonable timeout since webhook creation can take several seconds
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logger.Info("Polling for webhook configuration creation")

	// Helper function to check webhook status
	checkWebhooks := func() bool {
		for _, webhook := range expectedWebhooks {
			if foundWebhooks.Has(webhook.name) {
				continue
			}

			var err error
			if webhook.webhookType == base.MutatingWebhook {
				_, err = mutatingClient.Get(ctx, webhook.name, metav1.GetOptions{})
			} else {
				_, err = validatingClient.Get(ctx, webhook.name, metav1.GetOptions{})
			}
			if err == nil {
				foundWebhooks.Insert(webhook.name)
				logger.Info("Webhook configuration became available", "webhook", webhook.name, "type", webhook.webhookType)
			}
		}
		return foundWebhooks.IsSuperset(expectedWebhookSet)
	}

	// Check immediately first - don't waste 500ms waiting for ticker
	if checkWebhooks() {
		logger.Info("All webhook configurations are ready")
		return nil
	}

	// Not all ready yet, start polling
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			missing := expectedWebhookSet.Difference(foundWebhooks)
			return fmt.Errorf("timeout waiting for webhook configurations to be created: missing %v", sets.List(missing))
		case <-ticker.C:
			if checkWebhooks() {
				logger.Info("All webhook configurations are ready")
				return nil
			}
		}
	}
}

// unreadyComponentsFromError extracts unready component names from a control plane error.
// There doesn't seem to be any simple way to get a metav1.Status from the Go client, so we get
// the content in a string-formatted error, unfortunately.
func unreadyComponentsFromError(err error) sets.Set[string] {
	innerErr := strings.TrimPrefix(strings.TrimSuffix(err.Error(), `") has prevented the request from succeeding`), `an error on the server ("`)
	unreadyComponents := sets.New[string]()
	for _, line := range strings.Split(innerErr, `\n`) {
		if name := strings.TrimPrefix(strings.TrimSuffix(line, ` failed: reason withheld`), `[-]`); name != line {
			// NB: sometimes the error we get is truncated (server-side?) to something like:
			// `\n[-]poststar") has prevented the request from succeeding` # spellchecker:ignore
			// In those cases, the `name` here is also truncated, but nothing we can do about that.
			// For that reason, the list of components returned is not durable and should not be parsed.
			unreadyComponents.Insert(name)
		}
	}
	return unreadyComponents
}

// isCertificateError checks if an error is related to x509 certificate verification.
func isCertificateError(err error) bool {
	if err == nil {
		return false
	}

	// Check for x509 certificate verification errors
	var x509Err x509.CertificateInvalidError
	if errors.As(err, &x509Err) {
		return true
	}

	var unknownAuthorityErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityErr) {
		return true
	}

	var hostNameErr x509.HostnameError
	if errors.As(err, &hostNameErr) {
		return true
	}

	var constraintErr x509.ConstraintViolationError
	if errors.As(err, &constraintErr) {
		return true
	}

	// Fallback: check if the error message contains x509 certificate verification strings
	// This handles cases where the error is wrapped and doesn't directly expose the x509 types
	errMsg := err.Error()
	return strings.Contains(errMsg, "failed to verify certificate") ||
		strings.Contains(errMsg, "x509:")
}
