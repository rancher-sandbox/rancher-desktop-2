// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

// Interrupt keys namespace a process's interrupt event by role (and, for
// hostagents, by instance), so an IsOurProcess check confirms not only that a
// process is ours but that it plays the expected role. Only long-lived processes
// that record a PID register a handler: the control-plane daemon and each
// hostagent. On Windows the key becomes part of the event name; on Unix the key
// is ignored (Interrupt sends SIGINT and IsOurProcess is a no-op).

// ServeInterruptKey is the interrupt key for the control-plane daemon
// (`rdd service serve`).
const ServeInterruptKey = "serve"

// HostagentInterruptKey returns the interrupt key for the hostagent of the named
// Lima instance. Including the instance (not just the PID) means a recycled PID
// belonging to a *different* instance's hostagent is not mistaken for this one.
// Lima instance names are simple identifiers used as directory names, so they
// are safe to embed in a Windows event name (no backslash).
func HostagentInterruptKey(instance string) string {
	return "hostagent-" + instance
}
