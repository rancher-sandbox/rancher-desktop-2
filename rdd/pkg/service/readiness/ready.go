// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

// Package readiness waits for the embedded API server to become ready by
// polling its /readyz endpoint.
package readiness

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// WaitForReady polls the API server's /readyz endpoint until it succeeds.
func WaitForReady(ctx context.Context, config *rest.Config) error {
	logger := klog.FromContext(ctx)
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
				logger.Info("Control plane not ready", "err", err, "unreadyComponents", sets.List[string](unreadyComponents))
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
		logger.V(1).Info("Control plane not ready", "status", rc, "unreadyComponents", sets.List[string](lastSeenUnready))

		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// unreadyComponentsFromError extracts unready component names from a control plane error.
// There doesn't seem to be any simple way to get a metav1.Status from the Go client, so we get
// the content in a string-formatted error, unfortunately.
func unreadyComponentsFromError(err error) sets.Set[string] {
	innerErr := strings.TrimPrefix(strings.TrimSuffix(err.Error(), `") has prevented the request from succeeding`), `an error on the server ("`)
	unreadyComponents := sets.New[string]()
	for line := range strings.SplitSeq(innerErr, `\n`) {
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
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}

	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}

	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}

	if _, ok := errors.AsType[x509.ConstraintViolationError](err); ok {
		return true
	}

	// Fallback: check if the error message contains x509 certificate verification strings
	// This handles cases where the error is wrapped and doesn't directly expose the x509 types
	errMsg := err.Error()
	return strings.Contains(errMsg, "failed to verify certificate") ||
		strings.Contains(errMsg, "x509:")
}
