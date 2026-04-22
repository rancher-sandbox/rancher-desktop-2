#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors


# This script runs check-spelling on the parent repository.

set -o errexit -o nounset

check_prerequisites() {
    case $(uname -s) in # BSD uname doesn't support long option `--kernel-name`
        Darwin) check_prerequisites_darwin;;
        Linux) check_prerequisites_linux;;
        CYGWIN*|MINGW*|MSYS*) check_prerequisites_windows;;
        *) printf "Prerequisites not checked on %s\n" "$(uname -s)" >&2 ;;
    esac
}

check_prerequisites_darwin() {
    if command -v cpanm &>/dev/null; then
        return
    fi
    echo "Please install cpanminus first:" >&2
    if command -v brew &>/dev/null; then
        echo "brew install cpanminus" >&2
    fi
    exit 1
}

check_prerequisites_linux() {
    if ! command -v cpanm &>/dev/null; then
        echo "Please install cpanminus first:" >&2
        if command -v zypper &>/dev/null; then
            echo "zypper install perl-App-cpanminus perl-HTTP-Date perl-URI perl-YAML-PP" >&2
        elif command -v apt &>/dev/null; then
            echo "apt install cpanminus" >&2
        fi
        exit 1
    fi
}

check_prerequisites_windows() {
    # cygwin, mingw, msys (WSL2 uses the Linux path).
    echo "Skipping spell checking, Windows is not supported; please use WSL instead."
    exit
}

find_script() {
    local script
    script=$(dirname "${BASH_SOURCE[0]}")/../.github/actions/spelling/check-spelling/unknown-words.sh

    if [[ ! -x "$script" ]]; then
        printf "Failed to find check-spelling script %s - please run \`git submodule update --init\`\n" "$script" >&2
        exit 1
    fi

    echo "$script"
}

check_prerequisites
script=$(find_script)
warnings=(
    bad-regex
    binary-file
    deprecated-feature
    large-file
    limited-references
    no-newline-at-eof
    noisy-file
    non-alpha-in-dictionary
    token-is-substring
    unexpected-line-ending
    whitespace-in-dictionary
    minified-file
    unsupported-configuration
    no-files-to-check
)

INPUTS=$(yq --output-format=json <<EOF
    suppress_push_for_open_pull_request: 1
    checkout: false
    check_file_names: 1
    post_comment: 0
    use_magic_file: 1
    report-timing: 1
    warnings: $(IFS=,; echo "${warnings[*]}")
    ignore-next-line: |
        no-spell-check-next-line
    use_sarif: ${CI:-0}
    extra_dictionary_limit: 20
    extra_dictionaries:
        cspell:en_CA/src/hunspell-en_CA-large/en_CA-large.dic
        cspell:software-terms/dict/softwareTerms.txt
        cspell:aws/aws.txt
        cspell:cpp/src/stdlib-cmath.txt
        cspell:css/dict/css.txt
        cspell:docker/src/docker-words.txt
        cspell:filetypes/filetypes.txt
        cspell:fullstack/dict/fullstack.txt
        cspell:golang/dict/go.txt
        cspell:html/dict/html.txt
        cspell:k8s/dict/k8s.txt
        cspell:node/dict/node.txt
        cspell:npm/dict/npm.txt
        cspell:shell/dict/shell-all-words.txt
        cspell:typescript/dict/typescript.txt
EOF
)

export INPUTS

if [[ -z "${GITHUB_STEP_SUMMARY:-}" ]]; then
    # check-spelling falls over without this set; it writes to this file.
    export GITHUB_STEP_SUMMARY=/dev/null
fi

exec "$script"
