# GPU Passthrough (AMD ROCm on WSL2)

> **Status: experimental.** Targets AMD ROCm on WSL2 (Windows) only. Enabled
> per-instance with `spec.gpu.enabled` (see [App API](api_app.md)).

## What this enables

Container workloads running in the instance's k3s can use the host AMD GPU for
**HSA/HIP compute** — for example `rocminfo`, raw HIP kernels, `llama.cpp`, and
Ollama. This has been confirmed end-to-end on an AMD Ryzen AI Max+ 395 "Strix
Halo" (Radeon 8060S, gfx1151).

## How it works

On Windows, WSL2 does not expose the native Linux AMD kernel interface
(`/dev/kfd` + the amdgpu DRM stack). Instead it projects Microsoft's DirectX
Graphics kernel device, **`/dev/dxg`**, into every WSL distro, and AMD's
"ROCm on WSL" runtime talks to the GPU through it. Two extra userspace files
bridge ROCm to `/dev/dxg`:

- `librocdxg.so` — the ROCm ⇆ DXG bridge library.
- `dids.conf` — a supplemental PCI device-ID table used by that bridge.

These two files ship **only** with a ROCm-on-WSL install. They are *not*
projected by WSL (which only provides `libd3d12*.so` / `libdxcore.so`), *not*
present in the Windows driver store, and *not* bundled in ROCm container images.
So RDD must deliver them into the guest.

### File delivery

When `spec.gpu.enabled` is `true` on a Windows host, the App controller stages
the two files into the host-side instance directory (`instance.GPUDir()`), which
is inside the mounted host home and therefore readable from within the Lima VM:

1. **Fetch (default):** download AMD's official, MIT-licensed `rocdxg-roct`
   Debian package from the pinned [`ROCm/librocdxg`](https://github.com/ROCm/librocdxg)
   release, verify its SHA-256, and extract `librocdxg.so` + `dids.conf`. The
   pinned version lives in `pkg/gpu` (`Version`, `DebURL`, `DebSHA256`); bump
   them together to move to a newer release. Staging is idempotent — a version
   marker means an already-staged directory does no network I/O.
2. **Source override:** if `spec.gpu.source` points at a host directory holding
   the two files, they are copied from there instead (for air-gapped hosts or an
   existing ROCm-on-WSL install).

Provisioning (a `mode: system` step in `lima-template.yaml`, driven by the
`GPU_ENABLED` / `GPU_LIB_SOURCE` params) then installs the staged files into the
guest's `/opt/rocm/lib/librocdxg.so` and `/opt/rocm/share/rocdxg/dids.conf` on
every boot. When passthrough is disabled the same step **removes** them, so the
toggle is symmetric.

The download happens **host-side** (in the controller), not in the VM, because
the RDD host has reliable network access while the guest's outbound DNS can be
unreliable.

## The pod contract

Enabling `spec.gpu.enabled` only makes the bridge files available in the guest.
A workload pod must still opt in to the GPU explicitly. The proven contract is:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: rocm-smoke
spec:
  hostIPC: true
  containers:
  - name: rocm
    image: rocm/dev-ubuntu-22.04:7.2.4   # a gfx1151-capable ROCm image
    command: ["rocminfo"]
    securityContext:
      privileged: true                   # grants access to /dev/dxg
    env:
    - name: HSA_ENABLE_DXG_DETECTION
      value: "1"
    - name: LD_LIBRARY_PATH
      value: /usr/lib/wsl/lib:/opt/rocm/lib
    volumeMounts:
    - { name: wsl-lib,  mountPath: /usr/lib/wsl/lib, readOnly: true }
    - { name: librocdxg, mountPath: /opt/rocm/lib/librocdxg.so, readOnly: true }
    - { name: dids,      mountPath: /opt/rocm/share/rocdxg/dids.conf, readOnly: true }
  volumes:
  - { name: wsl-lib,   hostPath: { path: /usr/lib/wsl/lib } }
  - { name: librocdxg, hostPath: { path: /opt/rocm/lib/librocdxg.so } }
  - { name: dids,      hostPath: { path: /opt/rocm/share/rocdxg/dids.conf } }
```

Key points:

- `/dev/dxg` reaches the container via `privileged: true`.
- `/usr/lib/wsl/lib` (WSL-projected DirectX/dxcore libs) plus the two bridge
  files are bind-mounted from the guest.
- The image must contain a ROCm userspace built for the host GPU's ISA
  (gfx1151 for Strix Halo); the bridge files do not provide ROCm itself.

## Scope boundary and limitations

This feature enables the **HSA/HIP compute path** on WSL. It does **not**
provide the native kfd interface (`/dev/kfd`, `/sys/class/kfd` topology,
`amd-smi`, `rocprofiler`). Consequently:

- ✅ **Works:** `rocminfo`, raw HIP kernels, `llama.cpp`, Ollama — anything that
  uses HSA/HIP for compute.
- ❌ **Does not work:** the upstream [ROCm/k8s-device-plugin](https://github.com/ROCm/k8s-device-plugin)
  (it enumerates GPUs via `/dev/kfd`, absent on WSL), AMD's GPU Operator, and
  AMD **AIM** microservice charts. AIM images fail at runtime because their
  telemetry stack (`rocprofiler` + `amd-smi`) requires the native kfd topology
  that WSL cannot provide — a limitation *below* the Kubernetes layer, not
  something scheduling changes can fix.

Because the standard `amd.com/gpu` extended resource cannot be advertised on
WSL, workloads must declare the pod contract above directly (there is no device
plugin in this PR).

### Other known loose ends

- Files are re-installed by provisioning on every boot, so a guest reprovision
  does not lose them.
- Enabling on a non-Windows host is a no-op (the toggle resolves to
  `GPU_ENABLED: false`).

## Future work

- **WSL/DXG device plugin (ergonomics):** a small custom Kubernetes device
  plugin that advertises `amd.com/gpu` and, on `Allocate`, injects the full
  contract (device `/dev/dxg`, the WSL lib mounts, and the env vars) — so
  workloads can request `amd.com/gpu: 1` instead of hand-writing the pod spec,
  and potentially without `privileged`. The Device Plugin API is
  device-agnostic, so this is viable even though the upstream `/dev/kfd`-based
  plugin is not. This only improves ergonomics for the HSA/HIP workloads that
  already work; it does not make AIM-class (kfd-dependent) images run.
- **Wider tooling:** AMD also publishes a WSL-specific `amd-smi` package
  alongside `librocdxg`; investigating whether it (and any future kfd shim)
  widens the set of runnable workloads.
