// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command fake-kube-server fakes both endpoints the kuberlr resolver
// touches: the kubernetes apiserver's /version (which decides whether to
// download) and the release mirror's /release/* tree (which serves the
// fake kubectl). One server handles both roles so a single port covers
// both KUBECONFIG and RDD_KUBECTL_MIRROR.
//
// The server picks an ephemeral port, writes it to the file named by
// --port-file, and logs every request to the file named by --log-file
// (one "METHOD path" per line). The BATS test reads the port back to
// build the kubeconfig, then greps the log to assert which downloads
// happened on each test run.
//
// Two override files let individual tests change /version's behavior
// without restarting the server. Each request reads the file fresh, so
// a test can drop the file in setup() and let the next request pick it
// up. A missing or empty file falls back to the --git-version flag and
// HTTP 200.
//
//	--git-version-file:    one-line gitVersion string (e.g. "v1.99.1")
//	--version-status-file: one-line HTTP status code (e.g. "500")
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

func main() {
	root := flag.String("root", "", "directory served under /release/")
	major := flag.String("major", "1", "kubernetes server Major version")
	minor := flag.String("minor", "99", "kubernetes server Minor version")
	gitVersion := flag.String("git-version", "v1.99.0", "default kubernetes server gitVersion")
	gitVersionFile := flag.String("git-version-file", "", "optional file overriding gitVersion at request time")
	versionStatusFile := flag.String("version-status-file", "", "optional file overriding /version HTTP status at request time")
	portFile := flag.String("port-file", "", "file to receive the assigned port")
	logFile := flag.String("log-file", "", "file to receive one request line per request")
	flag.Parse()
	if *root == "" || *portFile == "" || *logFile == "" {
		log.Fatal("--root, --port-file, and --log-file are required")
	}

	// O_APPEND so per-test truncation (`: > log`) by BATS is honored:
	// without it, the server's fd position drifts past the new EOF and
	// writes land in a sparse hole that grep treats as binary noise.
	logFD, err := os.OpenFile(*logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("opening log file: %v", err)
	}
	defer logFD.Close()
	var logMu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		recordRequest(logFD, &logMu, r)
		status := readIntFile(*versionStatusFile, http.StatusOK)
		gv := readStringFile(*gitVersionFile, *gitVersion)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = fmt.Fprintf(w,
				`{"major":%q,"minor":%q,"gitVersion":%q,"gitCommit":"fake","gitTreeState":"clean","buildDate":"2026-01-01T00:00:00Z","goVersion":"go1.21","compiler":"gc","platform":"%s/%s"}`,
				*major, *minor, gv, runtime.GOOS, runtime.GOARCH,
			)
		}
	})
	mux.Handle("/release/", logging(logFD, &logMu, http.StripPrefix("/release/", http.FileServer(http.Dir(filepath.Join(*root, "release"))))))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(*portFile, []byte(strconv.Itoa(port)), 0o644); err != nil {
		log.Fatalf("writing port file: %v", err)
	}

	server := &http.Server{Handler: mux}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// logging wraps h so every request lands in the request log before h sees
// it. The release file server does not get to bypass logging just because
// it sits behind a StripPrefix.
func logging(fd *os.File, mu *sync.Mutex, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordRequest(fd, mu, r)
		h.ServeHTTP(w, r)
	})
}

func recordRequest(fd *os.File, mu *sync.Mutex, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(fd, "%s %s\n", r.Method, r.URL.Path)
}

// readStringFile returns the trimmed contents of path, or fallback when
// path is empty, missing, or unreadable.
func readStringFile(path, fallback string) string {
	if path == "" {
		return fallback
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return fallback
	}
	return s
}

// readIntFile returns the parsed integer in path, or fallback when path
// is empty, missing, unreadable, or unparseable.
func readIntFile(path string, fallback int) int {
	s := readStringFile(path, "")
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
