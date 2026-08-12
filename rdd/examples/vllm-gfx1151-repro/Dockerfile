# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# vLLM on AMD gfx1151 (Strix Halo) via Rancher Desktop Daemon / WSL /dev/dxg.
#
# Bakes the amdsmi shim into AMD's prebuilt gfx1151 vLLM image so the pod spec
# needs no configmap / PYTHONPATH juggling. See amdsmi.py for why the shim is
# required on WSL.
#
# The base image is large (~10 GB). Build it on the SAME WSL distro that RDD
# uses so the resulting image lands in the k3s containerd image store and the
# pod can start without an external pull.
#
# Pin: ROCm 7.13 / PyTorch 2.10 / vLLM 0.19.1, prebuilt for gfx1151. If you use
# a different GFX part, pick the matching rocm/vllm tag AND edit GFX_ARCH in
# amdsmi.py accordingly.
FROM rocm/vllm:rocm7.13.0_gfx1151_ubuntu24.04_py3.13_pytorch_2.10.0_vllm_0.19.1

# Shadow the real amdsmi package: a directory earlier on PYTHONPATH wins over
# the site-packages `amdsmi`, so `import amdsmi` resolves to our stub.
COPY amdsmi.py /shim/amdsmi.py
ENV PYTHONPATH=/shim
