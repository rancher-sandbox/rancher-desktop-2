#!/usr/bin/env bash

# assert_output_ge - Assert that the integer value of $output is greater than or equal to expected
assert_output_ge() {
    local -i expected=$1
    local -i actual

    if [[ ${output} =~ ^-?[0-9]+$ ]]; then
        actual="${output}"
    else
        # shellcheck disable=SC2312 # We're intentionally failing anyway.
        batslib_print_kv_single_or_multi 8 \
            'expected' ">= ${expected}" \
            'actual' "${output} (not a valid integer)" |
            batslib_decorate 'output does not contain a valid integer' |
            fail
        return $?
    fi

    if ((actual < expected)); then
        # shellcheck disable=SC2312 # We're intentionally failing anyway.
        batslib_print_kv_single_or_multi 8 \
            'expected' ">= ${expected}" \
            'actual' "${actual}" |
            batslib_decorate 'output is less than expected minimum' |
            fail
    fi
}

# assert_output_lt - Assert that the integer value of $output is less than expected
assert_output_lt() {
    local -i expected=$1
    local -i actual

    if [[ ${output} =~ ^-?[0-9]+$ ]]; then
        actual="${output}"
    else
        # shellcheck disable=SC2312 # We're intentionally failing anyway.
        batslib_print_kv_single_or_multi 8 \
            'expected' "< ${expected}" \
            'actual' "${output} (not a valid integer)" |
            batslib_decorate 'output does not contain a valid integer' |
            fail
        return $?
    fi

    if ((actual >= expected)); then
        # shellcheck disable=SC2312 # We're intentionally failing anyway.
        batslib_print_kv_single_or_multi 8 \
            'expected' "< ${expected}" \
            'actual' "${actual}" |
            batslib_decorate 'output is not less than expected maximum' |
            fail
    fi
}
