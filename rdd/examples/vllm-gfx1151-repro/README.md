# vLLM on AMD gfx1151 (Strix Halo) via Rancher Desktop Daemon

A self-contained, reproducible example that runs **vLLM (ROCm)** and serves an
OpenAI-compatible API on an **AMD Ryzen AI Max+ 395 "Strix Halo"** iGPU
(gfx1151, "AMD Radeon 8060S Graphics") through Rancher Desktop Daemon's (RDD)
k3s, using WSL's `/dev/dxg` GPU passthrough — **no NVIDIA, no device plugin**.

This was validated end-to-end: the server reaches `Application startup
complete` and `/v1/completions` returns coherent text generated on the GPU.

```
The capital of France is  ->  " Paris. It is the most populous city in Europe, ..."
```

## What's here

| File            | Purpose                                                                 |
|-----------------|-------------------------------------------------------------------------|
| `Dockerfile`    | Thin image over AMD's prebuilt [gfx1151 vLLM image](https://hub.docker.com/layers/rocm/vllm/rocm7.13.0_gfx1151_ubuntu24.04_py3.13_pytorch_2.10.0_vllm_0.19.1/images/sha256-394194d36edcf9b36bcb563e143b21b80e64e7d04f33a447b448c0c0c00c04a8); bakes in the shim.    |
| `amdsmi.py`     | The **amdsmi shim** — the one hack that makes vLLM work on WSL (see below why needed). |
| `vllm-pod.yaml` | Pod spec with the proven `/dev/dxg` contract; serves Qwen2.5-0.5B.       |
| `test.ps1`      | Windows verification (exec-based; avoids the `socat` port-forward issue).|

## Prerequisites (host-side — cannot be baked into the image)

These are environmental and must already be true on the machine running RDD:

1. **Rancher Desktop Daemon** running with its k3s, on **Windows 11 + WSL2**.
2. An **AMD gfx1151** GPU exposed to WSL via `/dev/dxg` (Strix Halo / Radeon 8060S).
3. The WSL/ROCm GPU bridge files present **on the RDD guest**, because the pod
   host-mounts them:
   - `/usr/lib/wsl/lib`               (WSL GPU user-mode libs)
   - `/opt/rocm/lib/librocdxg.so`     (the `/dev/dxg` HSA bridge; RDD's GPU
     feature stages this, pinned `librocdxg 1.2.2` in this repo's `pkg/gpu`)
   - `/opt/rocm/share/rocdxg/dids.conf`
   If your guest stages these elsewhere, edit the `hostPath`s in `vllm-pod.yaml`.
4. A working `docker` (or `nerdctl`) **inside that same WSL distro** to build the
   image.

## Build

The base image is **~10 GB**. Build on the **same WSL distro RDD uses** so the
image lands in the k3s containerd store and the pod starts without an external
pull. (`vllm-pod.yaml` sets `imagePullPolicy: Never` Intentional to use local)

```bash
# from this directory, inside the RDD WSL distro
docker build -t vllm-gfx1151:local .
```

## Deploy

```bash
rdd kubectl apply -f vllm-pod.yaml
rdd kubectl logs -f vllm-gfx1151    # watch for platform=rocm, weights load, "Application startup complete"
```

## Verify

```powershell
# Windows
./test.ps1
```

Success looks like:
- `/v1/models` -> `200`, lists `Qwen/Qwen2.5-0.5B-Instruct`.
- `/v1/completions` -> `200`, coherent text ("... Paris ...").
- Server log: `POST /v1/completions ... 200 OK` and a
  `Avg generation throughput: N tokens/s ... GPU KV cache usage` line — the
  **GPU KV-cache** metric proves it ran on the GPU, not CPU.

## Cleanup

```bash
rdd kubectl delete -f vllm-pod.yaml
```

---

## Why the amdsmi shim is needed (the core finding)

vLLM's ROCm **platform detection** and its **GCN-arch resolution** call into
`amdsmi`, AMD's system-management library. `amdsmi` reads GPU topology from
`/sys/class/kfd`, which **does not exist under WSL's `/dev/dxg`** passthrough.
So `amdsmi_init()` throws (Error 34) and vLLM aborts before serving:

```
RuntimeError: Failed to infer device type
```

But `amdsmi` is only used for **identification/telemetry**. The actual compute
path (`torch.cuda.*`) is HSA-backed and works fine over `/dev/dxg`. So we
**shadow** `amdsmi` with a tiny stub (`amdsmi.py`) placed earlier on
`PYTHONPATH`, reporting the single dxg GPU. Compute is untouched.

One subtlety: `amdsmi_get_gpu_asic_info()` must return
`target_graphics_version = "gfx1151"`, because vLLM's
`_query_gcn_arch_from_amdsmi()` returns that field verbatim as `_GCN_ARCH`. If
it's empty, vLLM falls back to `torch.cuda` and hits a circular import issue at
module load. **For a different GFX part, change `GFX_ARCH` in `amdsmi.py`** and
pik the matching `rocm/vllm` tag in the `Dockerfile`.

## Why `port-forward` is avoided

`kubectl port-forward` requires `socat` on the k3s node; the RDD guest doesn't
ship it (`unable to do port forwarding: socat not found`). The test scripts
instead `kubectl exec` into the pod and call the API over `localhost`, which
uses the exec channel and needs no socat. (Alternatives: a NodePort Service on
the node IP, or install `socat` into the guest.)

## Notes / caveats

- **Version skew is OK:** host ROCm 7.2.4 + librocdxg 1.2.2 bridged a ROCm 7.13
  container fine — HSA enumerated the GPU correctly.
- **Throughput is low** (iGPU + shared memory + tiny cold model). This example
  proves **correctness**, not performance.
- **The upstream/SUSE [`vllm-openai`](https://apps.rancher.io/applications/vllm) chart image is CUDA-only** (its [Dockerfile](https://github.com/vllm-project/vllm/blob/b6553be1bc75f046b00046a4ad7576364d03c835/docker/Dockerfile#L11)
  target roots in `nvidia/cuda`; the ROCm build is a separate [Dockerfile](https://github.com/vllm-project/vllm/blob/b6553be1bc75f046b00046a4ad7576364d03c835/docker/Dockerfile.rocm#L8) with no
  `vllm-openai` target). No chart values toggle makes it AMD-native — you must
  substitute a ROCm image, which is exactly what this example does.
