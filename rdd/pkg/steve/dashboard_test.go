// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package steve

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"gotest.tools/v3/assert"
)

func file(contents string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(contents)}
}

func TestHandler(t *testing.T) {
	const port = 12345
	dashboard := fstest.MapFS{
		"index.html":   file(`<a href="http://127.0.0.1:6120/x">`),
		"js/app.js":    file(`fetch("https://127.0.0.1:6120/v1")`),
		"css/site.css": file(`/* ://127.0.0.1:6120/ */`),
	}
	onlyIndex := fstest.MapFS{"index.html": file("index")}
	empty := fstest.MapFS{}
	checksum, err := dashboardChecksum(dashboard)
	assert.NilError(t, err)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "api "+r.URL.Path)
	})

	cases := []struct {
		name        string
		files       fs.FS
		path        string
		ifNoneMatch string
		wantStatus  int
		wantBody    string
	}{
		{name: "root redirects", path: "/", wantStatus: http.StatusFound},
		{name: "explorer gets index", path: "/c/local/explorer", wantStatus: http.StatusOK, wantBody: `<a href="http://127.0.0.1:12345/x">`},
		{name: "js is rewritten", path: "/js/app.js", wantStatus: http.StatusOK, wantBody: `fetch("https://127.0.0.1:12345/v1")`},
		{name: "css is not rewritten", path: "/css/site.css", wantStatus: http.StatusOK, wantBody: `/* ://127.0.0.1:6120/ */`},
		{name: "local cluster prefix is stripped", path: "/k8s/clusters/local/v1/namespaces", wantStatus: http.StatusOK, wantBody: "api /v1/namespaces"},
		{name: "api path", path: "/v1/namespaces", wantStatus: http.StatusOK, wantBody: "api /v1/namespaces"},
		{name: "unknown path", path: "/v3/settings", wantStatus: http.StatusOK, wantBody: "api /v3/settings"},
		{name: "one-file dashboard is not a 304", files: onlyIndex, path: "/index.html", wantStatus: http.StatusOK, wantBody: "index"},
		{name: "empty dashboard is not a 304", files: empty, path: "/c/local/explorer", wantStatus: http.StatusNotFound, wantBody: "404 page not found\n"},
		{name: "matching tag", path: "/c/local/explorer", ifNoneMatch: `W/"` + checksum + `"`, wantStatus: http.StatusNotModified},
		{name: "star", path: "/c/local/explorer", ifNoneMatch: "*", wantStatus: http.StatusNotModified},
		{name: "different tag", path: "/js/app.js", ifNoneMatch: `W/"other"`, wantStatus: http.StatusOK, wantBody: `fetch("https://127.0.0.1:12345/v1")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			if files == nil {
				files = dashboard
			}
			handler, err := newHandler(api, files, port)
			assert.NilError(t, err)

			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, http.NoBody)
			request.Header.Set("If-None-Match", tc.ifNoneMatch)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			assert.Equal(t, recorder.Code, tc.wantStatus)
			if tc.wantStatus == http.StatusFound {
				assert.Equal(t, recorder.Header().Get("Location"), "/c/local/explorer")
			} else {
				assert.Equal(t, recorder.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestDashboardChecksumCoversContents(t *testing.T) {
	before, err := dashboardChecksum(fstest.MapFS{"index.html": file("one")})
	assert.NilError(t, err)
	after, err := dashboardChecksum(fstest.MapFS{"index.html": file("two")})
	assert.NilError(t, err)
	assert.Assert(t, before != after)
}
