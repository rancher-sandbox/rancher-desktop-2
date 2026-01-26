# PATH_BATS_ROOT, PATH_BATS_LOGS, and PATH_BATS_HELPERS are already set by load.bash

PATH_REPO_ROOT=$(absolute_path "${PATH_BATS_ROOT}/..")

inside_repo_clone() {
    [[ -d "${PATH_REPO_ROOT}/pkg/rancher-desktop-daemon" ]]
}

if is_macos; then
    PATH_APP_HOME="${HOME}/Library/Application Support/rancher-desktop-${RDD_INSTANCE}"
    PATH_CONFIG="${HOME}/Library/Preferences/rancher-desktop-${RDD_INSTANCE}"
    PATH_CACHE="${HOME}/Library/Caches/rancher-desktop-${RDD_INSTANCE}"
    PATH_LOGS="${PATH_APP_HOME}"
fi

if is_linux; then
    PATH_APP_HOME="${HOME}/.local/share/rancher-desktop-${RDD_INSTANCE}"
    PATH_CONFIG="${HOME}/.config/rancher-desktop-${RDD_INSTANCE}"
    PATH_CACHE="${HOME}/.cache/rancher-desktop-${RDD_INSTANCE}"
    PATH_LOGS="${PATH_APP_HOME}"
fi

# Get the WSL (Unix) path for a Windows shell folder id; for a list of valid ids,
# see https://learn.microsoft.com/en-us/dotnet/api/system.environment.specialfolder
wslpath_from_win32_folder_id() {
    local folder_id=$1
    local windows_path
    windows_path=$(powershell.exe -Command "[System.Environment]::GetFolderPath(${folder_id})")
    local unix_path
    unix_path=$(wslpath -u "${windows_path}")
    tr -d "\r" <<<"${unix_path}"
}

if is_windows; then
    LOCALAPPDATA="$(wslpath_from_win32_folder_id 28)"
    PROGRAMFILES="$(wslpath_from_win32_folder_id 38)"

    PATH_APP_HOME="${LOCALAPPDATA}/rancher-desktop-${RDD_INSTANCE}"
    PATH_CONFIG="${LOCALAPPDATA}/rancher-desktop-${RDD_INSTANCE}"
    PATH_CACHE="${PATH_APP_HOME}/cache"
    PATH_LOGS="${PATH_APP_HOME}"
    PATH_DISTRO="${PATH_APP_HOME}/distro"
    PATH_DISTRO_DATA="${PATH_APP_HOME}/distro-data"
fi

LIMA_HOME="${PATH_APP_HOME}/lima"
