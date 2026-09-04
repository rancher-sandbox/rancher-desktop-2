# Minimal amdsmi shim for running vLLM (ROCm) on WSL / Rancher Desktop Daemon (RDD).
#
# WHY THIS EXISTS
# ---------------
# vLLM's ROCm platform detection and its GCN-arch resolution call into `amdsmi`,
# AMD's system-management library. `amdsmi` reads GPU topology from
# /sys/class/kfd, which does NOT exist under WSL's /dev/dxg GPU passthrough.
# So `amdsmi_init()` throws (Error 34) and vLLM bails out with:
#     "RuntimeError: Failed to infer device type".
#
# amdsmi is only used for IDENTIFICATION/telemetry — the actual compute path
# goes through torch/HSA (torch.cuda.*), which works fine on /dev/dxg. So we
# shadow `amdsmi` with this stub (placed earlier on PYTHONPATH than the real
# site-packages package) that reports the single dxg GPU. Compute is untouched.
#
# CRITICAL: amdsmi_get_gpu_asic_info() must return target_graphics_version set
# to the real arch ("gfx1151" for Strix Halo / Radeon 8060S). vLLM's
# _query_gcn_arch_from_amdsmi() returns that field verbatim as _GCN_ARCH; if it
# is empty vLLM falls back to torch.cuda and hits a circular-import bug at load.
#
# If you run on a different RDNA/GFX part, change GFX_ARCH below.

GFX_ARCH = "gfx1151"


class AmdSmiException(Exception):
    pass


_HANDLE = 0


def amdsmi_init(*args, **kwargs):
    return None


def amdsmi_shut_down(*args, **kwargs):
    return None


def amdsmi_get_processor_handles():
    return [_HANDLE]


def amdsmi_get_gpu_asic_info(handle):
    return {
        "market_name": "AMD Radeon 8060S Graphics",
        "target_graphics_version": GFX_ARCH,
        "device_id": "0x1586",
        "vendor_id": "0x1002",
        "rev_id": "0x00",
        "asic_serial": "0x0",
    }


def amdsmi_get_gpu_board_info(handle):
    return {"product_name": "AMD Radeon 8060S Graphics"}


def amdsmi_topo_get_link_type(h1, h2):
    return {"type": 0, "hops": 0}


class AmdSmiInitFlags:
    INIT_AMD_GPUS = 1


def __getattr__(name):
    # Any other amdsmi_* symbol -> benign no-op returning an empty dict.
    if name.startswith("amdsmi_"):
        def _noop(*args, **kwargs):
            return {}

        return _noop
    raise AttributeError(name)
