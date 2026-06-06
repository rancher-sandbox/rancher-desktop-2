// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"github.com/go-logr/logr"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/linuxkit/virtsock/pkg/hvsock"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/windows/registry"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/socketbridge"
)

// hostSwitchPlatform holds the host-switch state for Windows. On non-Windows
// platforms, this is an empty struct (see hostswitch_other.go).
type hostSwitchPlatform struct {
	// hostSwitchMu protects hostSwitchStates. A separate mutex (not
	// instanceStatesMu) because the host-switch goroutine starts before
	// the watcher creates its instanceState entry.
	hostSwitchMu     sync.Mutex
	hostSwitchStates map[string]*hostSwitchState
}

// initHostSwitch initializes the host-switch state map.
func (p *hostSwitchPlatform) initHostSwitch() {
	p.hostSwitchStates = make(map[string]*hostSwitchState)
}

// hostSwitchState tracks a running host-switch goroutine for one VM instance.
type hostSwitchState struct {
	cancel context.CancelFunc
	done   chan struct{}

	// namespace of the owning LimaVM, captured so the goroutine can enqueue a
	// reconcile when it exits unexpectedly.
	namespace string

	// failed is set when the goroutine exits on error rather than cancellation.
	// The reconciler reads it via hostSwitchHealthy and restarts a failed bridge.
	failed atomic.Bool

	// lastRestart records when this bridge was last (re)started, so the reconciler
	// can rate-limit recovery restarts to one per hostSwitchRetryInterval.
	lastRestart time.Time
}

// Virtual network configuration for the host-switch. These values are a
// protocol contract with the guest-side binaries (network-setup, vm-switch)
// baked into the opensuse distro image.
const (
	defaultSubnet    = "192.168.127.0/24"
	tapDeviceMacAddr = "5a:94:ef:e4:0c:ee"
)

// hostSwitchSubnet holds the derived network addresses for the virtual network.
type hostSwitchSubnet struct {
	GatewayIP       string
	StaticDHCPLease map[string]string
	StaticDNSHost   string
	SubnetCIDR      string
}

// validateSubnet parses a CIDR subnet and derives the gateway, DHCP lease,
// and static DNS host addresses used by the virtual network.
func validateSubnet(subnet string) (*hostSwitchSubnet, error) {
	ip, _, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %w", subnet, err)
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil, fmt.Errorf("subnet %q is not IPv4", subnet)
	}
	tapIP := net.IPv4(ipv4[0], ipv4[1], ipv4[2], 2).String()
	return &hostSwitchSubnet{
		GatewayIP: net.IPv4(ipv4[0], ipv4[1], ipv4[2], 1).String(),
		StaticDHCPLease: map[string]string{
			tapIP: tapDeviceMacAddr,
		},
		StaticDNSHost: net.IPv4(ipv4[0], ipv4[1], ipv4[2], 254).String(),
		SubnetCIDR:    subnet,
	}, nil
}

// Vsock port assignments. These are protocol constants shared with the
// guest-side network-setup binary.
const (
	vsockHandshakePort = 6669
	vsockListenPort    = 6656
	handshakeTimeout   = 5 * time.Minute

	// relayMinDuration is how long a vsock data connection must last before we
	// treat it as a real relay. A connection that closes sooner likely carried
	// no frames; the byte counters in the diagnostics show whether traffic
	// actually flowed.
	relayMinDuration = 5 * time.Second
	// relayDropWarnThreshold is the number of consecutive short-lived
	// connections that escalates the quiet per-drop logging to a single error
	// carrying the diagnostics.
	relayDropWarnThreshold = 3

	// signaturePhrase identifies our distro among all running Hyper-V VMs.
	// This value is a protocol contract with the guest-side network-setup
	// binary and must not be changed independently.
	signaturePhrase = "github.com/rancher-sandbox/rancher-desktop/src/go/networking"
	readySignal     = "READY"

	gatewayMacAddr = "5a:94:ef:e4:0c:dd"
	defaultMTU     = 1500
)

// startHostSwitch launches the host-switch goroutine for a WSL2 instance.
// It must be called before the hostagent starts, because the guest's
// network-setup.service performs a vsock handshake during early boot.
func (r *LimaVMReconciler) startHostSwitch(ctx context.Context, name, namespace string, inst *limatype.Instance) {
	if inst.VMType != limatype.WSL2 {
		return
	}

	r.stopHostSwitch(name)

	state := &hostSwitchState{namespace: namespace}
	r.hostSwitchMu.Lock()
	r.hostSwitchStates[name] = state
	r.hostSwitchMu.Unlock()

	r.launchHostSwitch(ctx, name, state)
}

// launchHostSwitch starts the host-switch goroutine for an existing state entry
// and records the (re)start time. The caller holds no lock.
func (r *LimaVMReconciler) launchHostSwitch(ctx context.Context, name string, state *hostSwitchState) {
	hsCtx, hsCancel := context.WithCancel(ctx)
	done := make(chan struct{})

	r.hostSwitchMu.Lock()
	state.cancel = hsCancel
	state.done = done
	state.failed.Store(false)
	state.lastRestart = time.Now()
	r.hostSwitchMu.Unlock()

	logger := logr.FromContextOrDiscard(ctx).WithValues("instance", name, "component", "host-switch")
	go r.runHostSwitch(hsCtx, logger, name, state, done)
}

// restartHostSwitch relaunches a host-switch that exited unexpectedly while its
// VM keeps running. Restarts are rate-limited to one per
// hostSwitchRetryInterval. It returns true when it relaunches the bridge, false
// when none is tracked or the retry interval has not elapsed.
//
// Per-object reconciles are serialized, so no concurrent stopHostSwitch removes
// the entry between the unlock and launchHostSwitch.
func (r *LimaVMReconciler) restartHostSwitch(ctx context.Context, name string) bool {
	r.hostSwitchMu.Lock()
	state, ok := r.hostSwitchStates[name]
	if !ok || time.Since(state.lastRestart) < hostSwitchRetryInterval {
		r.hostSwitchMu.Unlock()
		return false
	}
	oldCancel, oldDone := state.cancel, state.done
	r.hostSwitchMu.Unlock()

	// Stop the dead goroutine before relaunching, mirroring stopHostSwitch's
	// cancel-then-wait outside the lock.
	if oldCancel != nil {
		oldCancel()
	}
	if oldDone != nil {
		<-oldDone
	}

	r.launchHostSwitch(ctx, name, state)
	return true
}

// hostSwitchHealthy reports whether the host-switch for an instance is alive. A
// missing entry counts as healthy: non-WSL2 instances never start a bridge, and
// a stopping VM has had its entry removed.
func (r *LimaVMReconciler) hostSwitchHealthy(name string) bool {
	r.hostSwitchMu.Lock()
	state, ok := r.hostSwitchStates[name]
	r.hostSwitchMu.Unlock()
	return !ok || !state.failed.Load()
}

// stopHostSwitch cancels the host-switch goroutine and waits for it to exit.
func (r *LimaVMReconciler) stopHostSwitch(name string) {
	r.hostSwitchMu.Lock()
	state, ok := r.hostSwitchStates[name]
	if ok {
		delete(r.hostSwitchStates, name)
	}
	r.hostSwitchMu.Unlock()
	if !ok {
		return
	}

	state.cancel()
	<-state.done
}

// runHostSwitch is the host-switch goroutine. It performs the vsock handshake,
// creates a gvisor-tap-vsock virtual network, and relays Ethernet frames
// between the host and the WSL2 VM until the context is cancelled.
//
// On an unexpected exit (an error rather than cancellation) the guest loses
// DHCP/DNS/NAT, so the goroutine marks the bridge failed and enqueues a
// reconcile; handleWatchedState then restarts it via restartHostSwitch.
func (r *LimaVMReconciler) runHostSwitch(ctx context.Context, logger logr.Logger, name string, state *hostSwitchState, done chan struct{}) {
	defer close(done)

	subnet, err := validateSubnet(defaultSubnet)
	if err != nil {
		logger.Error(err, "Invalid subnet configuration")
		return
	}

	ln, vmGUID, err := vsockHandshake(ctx, logger)
	if err != nil {
		logger.Error(err, "Vsock handshake failed")
		return
	}

	cfg := newVirtualNetworkConfig(*subnet)
	vn, err := virtualnetwork.New(&cfg)
	if err != nil {
		ln.Close()
		logger.Error(err, "Failed to create virtual network")
		return
	}
	defer unexposeAllForwards(logger, vn)

	// Set up the API listener before starting errgroup goroutines so a
	// failure here does not leak goroutines.
	apiAddr := fmt.Sprintf("%s:80", cfg.GatewayIP)
	vnLn, err := vn.Listen("tcp", apiAddr)
	if err != nil {
		ln.Close()
		logger.Error(err, "Failed to listen on API address", "addr", apiAddr)
		return
	}
	mux := http.NewServeMux()
	mux.Handle("/services/forwarder/all", vn.Mux())
	mux.Handle("/services/forwarder/expose", vn.Mux())
	mux.Handle("/services/forwarder/unexpose", vn.Mux())

	// Capture the parent context before errgroup shadows it: after g.Wait() the
	// errgroup's derived ctx is always cancelled, so only the parent reveals
	// whether this exit was a clean shutdown or an unexpected failure.
	parentCtx := ctx
	g, ctx := errgroup.WithContext(ctx)

	// Start the host-side socket bridge now that we have the VM GUID.
	// It listens on the Docker named pipe and forwards each connection to
	// rdd-guest inside the VM via vsock port 6660.  rdd-guest is baked into
	// the VM image (via rancher-desktop-opensuse) and started by systemd.
	g.Go(func() error {
		bridge := socketbridge.NewDockerHostBridge(vmGUID, logger)
		if err := bridge.Run(ctx); err != nil {
			logger.Error(err, "Socket bridge exited with error")
		}
		return nil
	})

	// Accept vsock connections and feed them into the virtual network.
	g.Go(func() error {
		var immediateDrops int
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil // Listener closed during shutdown.
				}
				return fmt.Errorf("vsock accept failed: %w", err)
			}
			// AcceptStdio blocks until the connection closes. This is
			// intentional: each VM runs a single vm-switch process, so
			// reconnections are serial (old connection EOF, then new accept).
			start := time.Now()
			err = vn.AcceptStdio(ctx, conn)
			elapsed := time.Since(start)
			switch {
			case err == nil:
				immediateDrops = 0
				logger.Info("Accepted vsock connection", "duration", elapsed.String())
			case elapsed < relayMinDuration:
				// A data connection that closes within relayMinDuration likely
				// relayed no frames. One short drop is routine churn, so log it
				// quietly; a run of them while the handshake keeps succeeding is
				// a dead data plane behind a live bridge, so escalate once with
				// the diagnostics attached.
				immediateDrops++
				logger.V(1).Info("Vsock data connection closed soon after opening",
					"duration", elapsed.String(), "consecutiveDrops", immediateDrops)
				if immediateDrops == relayDropWarnThreshold {
					// Duration is only a heuristic; the byte counters are the
					// evidence of whether any frames actually flowed.
					logger.Error(err, "Vsock data plane appears stalled: connections keep closing within seconds; if no frames are relaying the guest has no DHCP/DNS/NAT. Check the guest vm-switch",
						append([]any{"duration", elapsed.String(), "port", vsockListenPort},
							vnDiagnostics(vn)...)...)
				}
			default:
				immediateDrops = 0
				logger.Error(err, "Failed to accept connection into virtual network", "duration", elapsed.String())
			}
		}
	})

	// Close the vsock listener when the context is cancelled.
	g.Go(func() error {
		<-ctx.Done()
		return ln.Close()
	})

	g.Go(func() error {
		<-ctx.Done()
		return vnLn.Close()
	})
	g.Go(func() error {
		s := &http.Server{
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		err := s.Serve(vnLn)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})

	// Snapshot the CAM table and stack counters once a minute so a diagnostic
	// run (CI logs at trace) records the host-to-guest delivery state without a
	// debugger. The output is V(1) and vnDiagnostics queries the network on each
	// tick, so spawn the goroutine only when V(1) is enabled rather than ticking
	// and discarding the result.
	if vl := logger.V(1); vl.Enabled() {
		g.Go(func() error {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					vl.Info("Virtual network diagnostics", vnDiagnostics(vn)...)
				}
			}
		})
	}

	logger.Info("Host-switch running", "subnet", subnet.SubnetCIDR, "gateway", subnet.GatewayIP)

	switch err := g.Wait(); {
	case parentCtx.Err() != nil:
		// Cancelled by stopHostSwitch or restartHostSwitch: a clean shutdown.
		logger.Info("Host-switch stopped")
	case err != nil:
		// The bridge died on its own; the guest has no DHCP/DNS/NAT until it is
		// restarted. Flag it and wake the reconciler to recover.
		logger.Error(err, "Host-switch exited unexpectedly; guest networking is down until it is restarted")
		state.failed.Store(true)
		r.enqueueReconcile(name, state.namespace)
	default:
		logger.Info("Host-switch stopped")
	}
}

// newVirtualNetworkConfig builds the gvisor-tap-vsock configuration.
func newVirtualNetworkConfig(subnet hostSwitchSubnet) types.Configuration {
	return types.Configuration{
		MTU:               defaultMTU,
		Subnet:            subnet.SubnetCIDR,
		GatewayIP:         subnet.GatewayIP,
		GatewayMacAddress: gatewayMacAddr,
		DHCPStaticLeases:  subnet.StaticDHCPLease,
		DNS: []types.Zone{
			{
				Name: "rancher-desktop.internal.",
				Records: []types.Record{
					{Name: "gateway", IP: net.ParseIP(subnet.GatewayIP)},
					{Name: "host", IP: net.ParseIP(subnet.StaticDNSHost)},
				},
			},
			{
				Name: "docker.internal.",
				Records: []types.Record{
					{Name: "gateway", IP: net.ParseIP(subnet.GatewayIP)},
					{Name: "host", IP: net.ParseIP(subnet.StaticDNSHost)},
				},
			},
		},
		NAT: map[string]string{
			subnet.StaticDNSHost: "127.0.0.1",
		},
		GatewayVirtualIPs: []string{subnet.StaticDNSHost},
	}
}

// unexposeAllForwards closes every host listener the forwarder API opened on
// this virtual network. gvisor-tap-vsock never closes these listeners when the
// network is torn down, and the host-switch runs in the long-lived controller,
// so a port still exposed at teardown keeps its host port bound after the VM
// is gone: the next boot cannot re-expose it (EADDRINUSE), and connections
// route into the dead network with no guest attached ("no route to host")
// until rdd exits.
func unexposeAllForwards(logger logr.Logger, vn *virtualnetwork.VirtualNetwork) {
	var forwards []struct {
		Local    string `json:"local"`
		Protocol string `json:"protocol"`
	}
	all, err := requestVN(vn, http.MethodGet, "/services/forwarder/all", nil)
	if err != nil {
		logger.Error(err, "Failed to list exposed forwards for teardown")
		return
	}
	if err := json.Unmarshal(all.Body.Bytes(), &forwards); err != nil {
		logger.Error(err, "Failed to list exposed forwards for teardown")
		return
	}
	for _, fwd := range forwards {
		body, err := json.Marshal(fwd)
		if err != nil {
			logger.Error(err, "Failed to marshal forward for teardown",
				"local", fwd.Local, "protocol", fwd.Protocol)
			continue
		}
		rec, err := requestVN(vn, http.MethodPost, "/services/forwarder/unexpose", body)
		if err != nil {
			logger.Error(err, "Failed to unexpose forward at teardown",
				"local", fwd.Local, "protocol", fwd.Protocol)
			continue
		}
		logger.Info("Unexposed forward at teardown",
			"local", fwd.Local, "protocol", fwd.Protocol, "status", rec.Code)
	}
}

// requestVN performs an in-process request against the virtual network's
// services mux and returns the response recorder. The mux serves the forwarder
// API under /services/forwarder/. A nil body sends an empty request.
func requestVN(vn *virtualnetwork.VirtualNetwork, method, path string, body []byte) (*httptest.ResponseRecorder, error) {
	var r io.Reader = http.NoBody
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, path, r)
	if err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	vn.ServicesMux().ServeHTTP(rec, req)
	return rec, nil
}

// vnDiagnostics snapshots the host-to-guest delivery state from the stock /cam
// and /stats endpoints: the switch CAM table, ARP counters, and switch byte
// counters (a post-DHCP egress wedge freezes bytesReceived while the relay
// stays up). Returned as logr key-value pairs.
func vnDiagnostics(vn *virtualnetwork.VirtualNetwork) []any {
	var stats struct {
		ARP           map[string]any `json:"ARP"`
		BytesSent     uint64         `json:"BytesSent"`
		BytesReceived uint64         `json:"BytesReceived"`
	}
	arp := "unavailable"
	if rec, err := requestVN(vn, http.MethodGet, "/stats", nil); err == nil {
		if err := json.Unmarshal(rec.Body.Bytes(), &stats); err == nil {
			if b, err := json.Marshal(stats.ARP); err == nil {
				arp = string(b)
			}
		}
	}
	var cam string
	if rec, err := requestVN(vn, http.MethodGet, "/cam", nil); err == nil {
		cam = strings.TrimSpace(rec.Body.String())
	}
	return []any{
		"cam", cam,
		"bytesSent", stats.BytesSent,
		"bytesReceived", stats.BytesReceived,
		"arp", arp,
	}
}

// --- Vsock handshake ---

// vsockHandshake discovers the WSL2 VM via AF_VSOCK, exchanges a signature
// to verify identity, and returns a listener on the data port.
func vsockHandshake(ctx context.Context, logger logr.Logger) (net.Listener, hvsock.GUID, error) {
	hsCtx, hsCancel := context.WithTimeout(ctx, handshakeTimeout)
	defer hsCancel()

	vmGUID, err := getVMGUID(hsCtx, logger)
	if err != nil {
		return nil, hvsock.GUIDZero, fmt.Errorf("VM GUID discovery failed: %w", err)
	}

	logger.Info("Handshake succeeded", "vmGUID", vmGUID.String())

	ln, err := vsockListen(vmGUID, vsockListenPort)
	if err != nil {
		return nil, hvsock.GUIDZero, fmt.Errorf("vsock listen on port %d failed: %w", vsockListenPort, err)
	}

	if err := signalListenerReady(hsCtx, vmGUID); err != nil {
		ln.Close()
		return nil, hvsock.GUIDZero, fmt.Errorf("sending %s signal failed: %w", readySignal, err)
	}

	return ln, vmGUID, nil
}

// getVMGUID discovers the Hyper-V VM running our distro by polling the
// registry for VM GUIDs and handshaking with each in parallel. The registry
// is re-scanned every second so that VMs starting after the initial scan
// (e.g., the WSL2 utility VM on a fresh system) are discovered.
//
// The signature-based discovery assumes only one opensuse WSL2 instance
// runs at a time. With multiple instances, the first match wins and the
// others get no host-switch networking.
func getVMGUID(ctx context.Context, logger logr.Logger) (hvsock.GUID, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	found := make(chan hvsock.GUID, 1)
	seen := make(map[hvsock.GUID]bool)

	scanRegistry := func() {
		key, err := registry.OpenKey(
			registry.LOCAL_MACHINE,
			`SOFTWARE\Microsoft\Windows NT\CurrentVersion\HostComputeService\VolatileStore\ComputeSystem`,
			registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			return // Registry key not present yet; retry on next tick.
		}
		names, err := key.ReadSubKeyNames(0)
		key.Close()
		if err != nil {
			return
		}
		for _, name := range names {
			vmGUID, err := hvsock.GUIDFromString(name)
			if err != nil {
				logger.V(1).Info("Skipping invalid VM GUID", "name", name, "error", err)
				continue
			}
			if !seen[vmGUID] {
				seen[vmGUID] = true
				go attemptHandshake(ctx, logger, vmGUID, found)
			}
		}
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Immediate first scan, then rescan on each tick.
	scanRegistry()
	ticks := 0
	for {
		select {
		case vmGUID := <-found:
			return vmGUID, nil
		case <-ctx.Done():
			return hvsock.GUIDZero, fmt.Errorf("VM GUID discovery timed out: %w", ctx.Err())
		case <-ticker.C:
			ticks++
			// The dial failures inside attemptHandshake are V(1); without this
			// a bridge stuck waiting for the VM is silent at default verbosity.
			if ticks%30 == 0 {
				logger.Info("Still waiting for the WSL2 VM to answer the vsock handshake",
					"elapsed", (time.Duration(ticks) * time.Second).String())
			}
			scanRegistry()
		}
	}
}

// attemptHandshake polls a single VM GUID once per second, trying to match
// the signature phrase. Each probe runs synchronously to avoid goroutine
// leaks and panics from sending on a closed channel.
func attemptHandshake(ctx context.Context, logger logr.Logger, vmGUID hvsock.GUID, found chan<- hvsock.GUID) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		conn, err := getVsockConnection(vmGUID, vsockHandshakePort)
		if ctx.Err() != nil {
			if conn != nil {
				conn.Close()
			}
			return
		}
		if err != nil {
			logger.V(1).Info("Handshake dial failed", "vmGUID", vmGUID.String(), "error", err)
		} else {
			sig, err := readSignature(conn)
			conn.Close()
			if err != nil {
				logger.V(1).Info("Handshake read failed", "vmGUID", vmGUID.String(), "error", err)
			} else if sig == signaturePhrase {
				logger.V(1).Info("Signature matched", "vmGUID", vmGUID.String())
				select {
				case found <- vmGUID:
				default:
				}
				return
			} else {
				// Valid signature from a different distro; no point retrying.
				logger.V(1).Info("Signature mismatch", "vmGUID", vmGUID.String())
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// readSignature reads the signature phrase from the peer.
func readSignature(conn net.Conn) (string, error) {
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "", err
	}
	buf := make([]byte, len(signaturePhrase))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// signalListenerReady tells the guest that the data listener is ready.
// The dial is wrapped in a goroutine because hvsock.Dial does not accept
// a context. The select on ctx.Done prevents the caller from hanging, but
// the dial goroutine itself may leak if the VM becomes unresponsive.
func signalListenerReady(ctx context.Context, vmGUID hvsock.GUID) error {
	type result struct{ err error }
	ch := make(chan result, 1)
	go func() {
		conn, err := getVsockConnection(vmGUID, vsockHandshakePort)
		if err != nil {
			ch <- result{err}
			return
		}
		defer conn.Close()
		_, err = conn.Write([]byte(readySignal))
		ch <- result{err}
	}()
	select {
	case r := <-ch:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- Vsock connection helpers ---

// vsockListen creates an AF_VSOCK listener bound to a specific VM and port.
func vsockListen(vmGUID hvsock.GUID, port uint32) (net.Listener, error) {
	svcPort, err := hvsock.GUIDFromString(winio.VsockServiceID(port).String())
	if err != nil {
		return nil, fmt.Errorf("invalid vsock service GUID for port %d: %w", port, err)
	}
	return hvsock.Listen(hvsock.Addr{VMID: vmGUID, ServiceID: svcPort})
}

// getVsockConnection dials a vsock connection to the given VM and port.
func getVsockConnection(vmGUID hvsock.GUID, port uint32) (net.Conn, error) {
	svcPort, err := hvsock.GUIDFromString(winio.VsockServiceID(port).String())
	if err != nil {
		return nil, err
	}
	return hvsock.Dial(hvsock.Addr{VMID: vmGUID, ServiceID: svcPort})
}
