# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# SPDX-FileCopyrightText: The Lima Authors

# Format the string the way strconv.Quote() would do.
# If the input ends with an ellipsis then no closing quote will be added (and the … will be removed).
quote_msg() {
    local quoted
    quoted=$(sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/^/"/' <<<"$1")
    if [[ ${quoted} == *… ]]; then
        echo "${quoted%…}"
    else
        echo "${quoted}\""
    fi
}

assert_fatal() {
    local quoted
    quoted=$(quote_msg "$1")
    assert_stderr_line --partial "level=fatal msg=${quoted}"
}
assert_error() {
    local quoted
    quoted=$(quote_msg "$1")
    assert_stderr_line --partial "level=error msg=${quoted}"
}
assert_warning() {
    local quoted
    quoted=$(quote_msg "$1")
    assert_stderr_line --partial "level=warning msg=${quoted}"
}
assert_info() {
    local quoted
    quoted=$(quote_msg "$1")
    assert_stderr_line --partial "level=info msg=${quoted}"
}
assert_debug() {
    local quoted
    quoted=$(quote_msg "$1")
    assert_stderr_line --partial "level=debug msg=${quoted}"
}
