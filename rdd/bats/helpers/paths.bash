# PATH_BATS_ROOT, PATH_BATS_LOGS, and PATH_BATS_HELPERS are already set by load.bash

PATH_REPO_ROOT=$(absolute_path "${PATH_BATS_ROOT}/..")

inside_repo_clone() {
    [[ -d "${PATH_REPO_ROOT}/pkg/rancher-desktop-daemon" ]]
}

# Convert 'export KEY="C:\win\path"' to POSIX paths (WSL or MSYS2).
win_to_posix_exports() {
    tr -d "\r" | while IFS= read -r line; do
        if [[ "${line}" =~ ^(export\ [A-Z_]+=)\"(.+)\"$ ]]; then
            if command -v wslpath >/dev/null 2>&1; then
                printf '%s"%s"\n' "${BASH_REMATCH[1]}" "$(wslpath -u "${BASH_REMATCH[2]}")"
            else
                printf '%s"%s"\n' "${BASH_REMATCH[1]}" "$(cygpath -u "${BASH_REMATCH[2]}")"
            fi
        fi
    done
}

# Get instance paths from rdd (single source of truth).
# shellcheck disable=SC1090
if is_windows; then
    source <("${PATH_REPO_ROOT}/bin/rdd.exe" svc paths --output=shell | win_to_posix_exports)
else
    source <("${PATH_REPO_ROOT}/bin/rdd" svc paths --output=shell)
fi
