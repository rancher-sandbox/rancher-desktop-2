EXE=""
PLATFORM=${OS}
if is_windows; then
    if using_windows_exe; then
        EXE=".exe"
        PLATFORM=win32
    else
        # WSL with Linux binary (not yet supported).
        PLATFORM=linux
    fi
fi

no_cr() {
    tr -d '\r'
}
ctrctl() {
    if using_docker; then
        docker "$@"
    else
        nerdctl "$@"
    fi
}
curl() {
    command "curl${EXE}" "$@"
}

# Check if curl supports WebSockets; some tests may require it.
curl_has_websocket_support() {
    if ! command -v "curl${EXE}" >/dev/null 2>&1; then
        return 1
    fi
    local version
    version=$(curl --version 2>/dev/null)
    # `ws` may be the last protocol and precede a new line, so we only test for
    # space to the left of it.
    if [[ "${version}" =~ Protocols:.*\ wss? ]]; then
        return 0
    fi
    return 1
}

rdd() {
    local arg
    local args=("$@")
    if is_windows; then
        args=()
        if is_msys; then
            # MSYS_NO_PATHCONV is set globally to protect URL-like paths
            # (e.g. /passthrough/demo/hello), so convert filesystem paths
            # to Windows format manually.
            for arg in "$@"; do
                if [[ "${arg}" =~ ^/([a-zA-Z]/|tmp(/|$)) ]]; then
                    # Drive-letter mounts (/c/...) and /tmp are always
                    # filesystem paths; convert without existence check.
                    args+=("$(cygpath -w "${arg}")")
                elif [[ "${arg}" == /* ]] && [[ -e "${arg}" ]]; then
                    args+=("$(cygpath -w "${arg}")")
                else
                    args+=("${arg}")
                fi
            done
        else
            # WSL: convert /mnt/... paths.
            for arg in "$@"; do
                if [[ "${arg}" != "${arg#/mnt/}" ]]; then
                    args+=("$(wslpath -w "${arg}")")
                else
                    args+=("${arg}")
                fi
            done
            # Adjust WSLENV to include everything starting with RDD_
            local env envs
            mapfile -t envs < <({
                tr : '\n' <<<"${WSLENV}"
                env | awk -F= '/^RDD_/ { print $1 }'
            } | sort -u || true)
            WSLENV=""
            for env in "${envs[@]}"; do
                WSLENV="${WSLENV}:${env}"
            done
            WSLENV=${WSLENV#:}
            export WSLENV
        fi
    fi
    # Close BATS's internal fds 3 and 4 so any daemon rdd spawns (hostagent,
    # qemu) cannot inherit them. A grandchild that keeps fd 3 open will hang
    # bats when it next tries to capture output via run/$(...).
    "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${args[@]}" 3>&- 4>&-
}
