# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

local_setup_file() {
    setup_rdd_control_plane
}

@test 'Check that passthrough controllers work' {
    run -0 rdd ctl get --raw '/passthrough/demo/hello/thing'
    assert_line 'Hello, world!'
    assert_line --regexp 'X-Forwarded-For = .*127\.0\.0\.1.*'
    assert_line 'Path = "/thing"'
}

@test 'Check that passthrough to web socket works' {
    # We need to use curl for WebSocket support; however, some versions of curl
    # do not support ws:// URLs directly, so we check for that and skip the
    # test if not supported.
    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi
    run -0 rdd ctl config view --minify --flatten --output=jsonpath='{.clusters[].cluster.server}'
    local server_url=${output}
    run -0 rdd ctl config view --minify --flatten --output=jsonpath='{.users[].user.token}'
    local token=${output}
    run -0 curl --silent --verbose --header "Authorization: Bearer ${token}" --insecure \
        "${server_url/http/ws}/passthrough/demo/websocket/"
    assert_line --partial 'hello from websocket'
    assert_line --partial 'HTTP/1.1 101 Switching Protocols'
    assert_line --partial 'Connection: Upgrade'
    assert_line --partial 'Upgrade: websocket'
}
