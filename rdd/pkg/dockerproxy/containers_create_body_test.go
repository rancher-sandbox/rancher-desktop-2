// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package dockerproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestContainersCreateBody(t *testing.T) {
	decode := func(t *testing.T, buf []byte) any {
		var result any
		decoder := json.NewDecoder(bytes.NewReader(buf))
		decoder.UseNumber()
		assert.NilError(t, decoder.Decode(&result))
		return result
	}

	testCases := []struct {
		name string
		// input is the request body that the client sends.
		input string
		// change rewrites the body; nil if there is nothing to rewrite.
		change func(t *testing.T, body *containersCreateBody)
		// expected is the body that the proxy forwards, if change is set.
		expected string
	}{
		{
			name: "unknown fields are kept",
			input: `{
				"Image": "alpine", "Unknown": [1, {"a": null}],
				"HostConfig": {
					"Binds": ["/a:/a", "/b:/b"],
					"Unknown": true,
					"Huge": 9007199254740993,
					"Mounts": [
						{"Type": "bind", "Source": "/c", "Target": "/c", "ReadOnly": true,
						 "Consistency": "cached", "BindOptions": {"Propagation": "rshared"}},
						{"Type": "volume", "Source": "vol", "Target": "/v"}
					]
				}
			}`,
			change: func(t *testing.T, body *containersCreateBody) {
				body.Binds[1] = "/x:/b"
				assert.Assert(t, is.Len(body.Mounts, 2))
				assert.Equal(t, body.Mounts[0].Type, "bind")
				body.Mounts[0].Source = "/y"
			},
			expected: `{
				"Image": "alpine", "Unknown": [1, {"a": null}],
				"HostConfig": {
					"Binds": ["/a:/a", "/x:/b"],
					"Unknown": true,
					"Huge": 9007199254740993,
					"Mounts": [
						{"Type": "bind", "Source": "/y", "Target": "/c", "ReadOnly": true,
						 "Consistency": "cached", "BindOptions": {"Propagation": "rshared"}},
						{"Type": "volume", "Source": "vol", "Target": "/v"}
					]
				}
			}`,
		},
		{
			name:  "lowercase keys",
			input: `{"hostconfig": {"binds": ["/a:/a"], "mounts": [{"type": "bind", "source": "/c"}]}}`,
			change: func(t *testing.T, body *containersCreateBody) {
				assert.DeepEqual(t, body.Binds, []string{"/a:/a"})
				assert.Assert(t, is.Len(body.Mounts, 1))
				assert.Equal(t, body.Mounts[0].Type, "bind")
				assert.Equal(t, body.Mounts[0].Source, "/c")
				body.Binds[0] = "/x:/a"
				body.Mounts[0].Source = "/y"
			},
			expected: `{"hostconfig": {"binds": ["/x:/a"], "mounts": [{"type": "bind", "source": "/y"}]}}`,
		},
		{name: "absent HostConfig", input: `{"Image": "alpine"}`},
		{name: "null HostConfig", input: `{"HostConfig": null}`},
		{name: "null Binds and Mounts", input: `{"HostConfig": {"Binds": null, "Mounts": null}}`},
		{
			name:  "null mount",
			input: `{"HostConfig": {"Mounts": [null]}}`,
			change: func(t *testing.T, body *containersCreateBody) {
				assert.DeepEqual(t, body.Mounts, []*containersCreateMount{nil})
			},
			expected: `{"HostConfig": {"Mounts": [null]}}`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://nowhere.invalid/", bytes.NewBufferString(testCase.input))
			assert.NilError(t, err)
			body, err := readContainersCreateBody(req)
			assert.NilError(t, err)
			if testCase.change == nil {
				assert.Assert(t, is.Len(body.Binds, 0))
				assert.Assert(t, is.Len(body.Mounts, 0))
				return
			}
			testCase.change(t, body)
			assert.NilError(t, body.writeTo(req))
			buf, err := io.ReadAll(req.Body)
			assert.NilError(t, err)
			assert.Equal(t, req.ContentLength, int64(len(buf)))
			assert.DeepEqual(t, decode(t, buf), decode(t, []byte(testCase.expected)))
		})
	}

	t.Run("invalid JSON", func(t *testing.T) {
		input := `{"HostConfig": `
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://nowhere.invalid/", bytes.NewBufferString(input))
		assert.NilError(t, err)
		_, err = readContainersCreateBody(req)
		assert.ErrorContains(t, err, "could not unmarshal request body")
		buf, err := io.ReadAll(req.Body)
		assert.NilError(t, err)
		assert.Equal(t, string(buf), input)
	})

	t.Run("duplicate keys", func(t *testing.T) {
		for _, input := range []string{
			`{"HostConfig": {"Binds": ["/a:/a"]}, "HostConfig": {"Memory": 1}}`,
			`{"HostConfig": {}, "hostconfig": {"Binds": ["/a:/a"]}}`,
			`{"env": ["A=1"], "Env": ["A=2"], "HostConfig": {"Binds": ["/a:/a"]}}`,
			`{"HostConfig": {"Binds": ["/a:/a"], "binds": ["/b:/b"]}}`,
			`{"HostConfig": {"Mounts": [{"Type": "bind", "Source": "/a", "source": "/b"}]}}`,
		} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://nowhere.invalid/", bytes.NewBufferString(input))
			assert.NilError(t, err)
			_, err = readContainersCreateBody(req)
			assert.ErrorContains(t, err, "duplicate keys", input)
			buf, err := io.ReadAll(req.Body)
			assert.NilError(t, err)
			assert.Equal(t, string(buf), input)
		}
	})
}
