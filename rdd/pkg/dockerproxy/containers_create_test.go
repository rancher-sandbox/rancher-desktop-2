// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package dockerproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"gotest.tools/v3/assert"
)

// mungeBody runs mungeContainersCreate on a request with the given body and
// returns the body that the proxy forwards.
func mungeBody(t *testing.T, input string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://nowhere.invalid/containers/create", strings.NewReader(input))
	assert.NilError(t, err)
	assert.NilError(t, mungeContainersCreate(logr.Discard(), req))
	buf, err := io.ReadAll(req.Body)
	assert.NilError(t, err)
	return string(buf)
}

func TestMungeContainersCreateBinds(t *testing.T) {
	cases := []struct {
		bind string
		want string
	}{
		{`C:\src:/src`, "/mnt/c/src:/src"},
		{`C:\src:/src:ro`, "/mnt/c/src:/src:ro"},
		{`C:/src:/src`, "/mnt/c/src:/src"},
		{`d:\src:/src`, "/mnt/d/src:/src"},
		{"vol:/data", "vol:/data"},
		{"/unix:/data", "/unix:/data"},
		{`\\server\share:/s`, `\\server\share:/s`},
		{`\\?\C:\x:/x`, "/mnt/c/x:/x"},
		{`\\?\UNC\server\share:/s`, `\\?\UNC\server\share:/s`},
	}
	for _, tc := range cases {
		t.Run(tc.bind, func(t *testing.T) {
			input, err := json.Marshal(map[string]any{"HostConfig": map[string]any{"Binds": []string{tc.bind}}})
			assert.NilError(t, err)
			var got struct{ HostConfig struct{ Binds []string } }
			assert.NilError(t, json.Unmarshal([]byte(mungeBody(t, string(input))), &got))
			assert.DeepEqual(t, got.HostConfig.Binds, []string{tc.want})
		})
	}
}

func TestMungeContainersCreateMounts(t *testing.T) {
	input := `{"HostConfig": {"Mounts": [
		null,
		{"Type": "bind", "Source": "C:\\src", "Target": "/src", "ReadOnly": true},
		{"Type": "bind", "Source": "\\\\?\\C:\\x", "Target": "/x"},
		{"Type": "volume", "Source": "vol", "Target": "/v"},
		{"Type": "bind", "Source": "/unix", "Target": "/u"}
	]}}`
	want := `{"HostConfig": {"Mounts": [
		null,
		{"Type": "bind", "Source": "/mnt/c/src", "Target": "/src", "ReadOnly": true},
		{"Type": "bind", "Source": "/mnt/c/x", "Target": "/x"},
		{"Type": "volume", "Source": "vol", "Target": "/v"},
		{"Type": "bind", "Source": "/unix", "Target": "/u"}
	]}}`
	var got, expected any
	assert.NilError(t, json.Unmarshal([]byte(mungeBody(t, input)), &got))
	assert.NilError(t, json.Unmarshal([]byte(want), &expected))
	assert.DeepEqual(t, got, expected)
}

func TestMungeContainersCreateUnchangedBody(t *testing.T) {
	input := `{"Image":"alpine",  "Env":["A=1"], "HostConfig":{"Binds":["vol:/data", "/unix:/u"],
		"Mounts":[{"Type":"volume","Source":"vol","Target":"/v"}]}}`
	assert.Equal(t, mungeBody(t, input), input)
}
