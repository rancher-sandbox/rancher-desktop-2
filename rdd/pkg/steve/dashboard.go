// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package steve

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/textproto"
	"path"
	"strings"

	"github.com/sirupsen/logrus"
)

// dashboardFiles is the dashboard build, with index.html at its root. It is
// nil in a build without the with_dashboard tag.
var dashboardFiles fs.FS

// newHandler routes the dashboard's paths to files and everything else to api.
func newHandler(api http.Handler, files fs.FS, port int) (http.Handler, error) {
	checksum, err := dashboardChecksum(files)
	if err != nil {
		return nil, fmt.Errorf("could not checksum dashboard files: %w", err)
	}
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("could not read dashboard files: %w", err)
	}
	rewriter := rewriteHandler(port, files, checksum)

	mux := http.NewServeMux()
	mux.Handle("/", api)
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/c/local/explorer", http.StatusFound)
	})
	mux.Handle("/c/", rewriter)
	// The dashboard addresses the local cluster under this prefix. Websockets
	// cannot follow a 302, so serve it directly rather than redirecting.
	mux.Handle("/k8s/clusters/local/", http.StripPrefix("/k8s/clusters/local", api))
	// One route per top-level entry, so that unknown paths still reach api.
	for _, entry := range entries {
		if entry.IsDir() {
			mux.Handle("/"+entry.Name()+"/", rewriter)
		} else {
			mux.Handle("/"+entry.Name(), rewriter)
		}
	}
	return mux, nil
}

// dashboardChecksum returns a hex SHA-256 over the path and contents of every
// file. The result is never empty, which matters because a request without
// If-None-Match would match an empty checksum and get a 304.
func dashboardChecksum(files fs.FS) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		// The length marks where the contents end, so that bytes moved between
		// files change the hash.
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(contents))
		hash.Write(contents)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// rewriteHandler serves files, replacing the dashboard's hard-coded default
// port with the actual one.
func rewriteHandler(port int, files fs.FS, checksum string) http.HandlerFunc {
	fileServer := http.FileServerFS(files)
	etag := fmt.Sprintf(`W/"%s"`, checksum)
	replacer := strings.NewReplacer(
		"://127.0.0.1:6120/",
		fmt.Sprintf("://127.0.0.1:%d/", port))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		// A client holds a cached copy only for this origin, so the port in that
		// copy is already right.
		if isNotModified(r.Header.Get("If-None-Match"), checksum) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// The dashboard is a single-page application, so any path that is not a
		// file gets the index.
		actualPath := strings.TrimPrefix(r.URL.Path, "/")
		if stat, err := fs.Stat(files, actualPath); err != nil || stat.IsDir() {
			actualPath = "index.html"
		}
		// Only HTML and JavaScript use the hard-coded port when the page runs.
		switch path.Ext(actualPath) {
		case ".html", ".js":
		default:
			fileServer.ServeHTTP(w, r)
			return
		}

		contents, err := fs.ReadFile(files, actualPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
			} else {
				http.Error(w, fmt.Sprintf("could not read %s: %v", actualPath, err), http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(actualPath)))
		if _, err := replacer.WriteString(w, string(contents)); err != nil {
			logrus.Errorf("could not write response: %v", err)
		}
	}
}

// isNotModified reports whether an If-None-Match header matches desiredTag.
func isNotModified(ifNoneMatch, desiredTag string) bool {
	for tag := range strings.SplitSeq(ifNoneMatch, ",") {
		tag = textproto.TrimString(tag)
		if tag == "*" {
			return true
		}
		tag = strings.Trim(strings.TrimPrefix(tag, "W/"), `"`)
		if tag == desiredTag {
			return true
		}
	}
	return false
}
