// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// The instance implementation uses a lot of `sync.OnceValue`; when testing the
// various cases, we need to simulate resetting that state.  We do this by
// running the test in a subprocess with a clean environment.
func runTestProcess(t *testing.T, testFunc func(*testing.T), variables ...string) {
	t.Helper()

	const subprocessMarker = "RDD_INSTANCE_TEST_IS_SUBPROCESS"
	if os.Getenv(subprocessMarker) == subprocessMarker {
		// We are already in the subprocess, so just run the test.
		testFunc(t)
		return
	}

	// Copy the environment, but exclude relevant variables.
	excludes := []string{
		"RDD_INSTANCE=",
		"RDD_LOG_DIR=",
	}
	env := []string{subprocessMarker + "=" + subprocessMarker}
	for _, v := range os.Environ() {
		if !slices.ContainsFunc(excludes,
			func(s string) bool { return strings.HasPrefix(v, s) },
		) {
			env = append(env, v)
		}
	}
	env = append(env, variables...)

	args := append(slices.Clone(os.Args[1:]),
		fmt.Sprintf("-test.run=^%s$", regexp.QuoteMeta(t.Name())))
	cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
	cmd.Env = env
	cmd.Stdout = t.Output()
	cmd.Stderr = t.Output()
	assert.NilError(t, cmd.Run())
}

func TestSuffix(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, "2", Suffix())
		})
	})

	t.Run("custom", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, "test", Suffix())
		}, "RDD_INSTANCE=test")
	})
}

func TestIndex(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, 2, Index())
		})
	})
	t.Run("custom numeric", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, 42, Index())
		}, "RDD_INSTANCE=42")
	})
	t.Run("custom non-numeric", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, 198, Index())
		}, "RDD_INSTANCE=cc") // 'c' = 99
	})
}

func TestName(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, "rancher-desktop-2", Name())
		})
	})

	t.Run("custom", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			assert.Equal(t, "rancher-desktop-test", Name())
		}, "RDD_INSTANCE=test")
	})
}

func TestDir(t *testing.T) {
	cases := []string{"", "test"}
	for _, c := range cases {
		t.Run(fmt.Sprintf("instance=%q", c), func(t *testing.T) {
			runTestProcess(t, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home) // For Windows
				name := "rancher-desktop-2"
				if c != "" {
					t.Setenv("RDD_INSTANCE", c)
					name = fmt.Sprintf("rancher-desktop-%s", c)
				}
				expected := map[string]string{
					"windows": filepath.Join(home, "AppData", "Local", name),
					"linux":   filepath.Join(home, ".local", "share", name),
					"darwin":  filepath.Join(home, "Library", "Application Support", name),
				}[runtime.GOOS]
				assert.Equal(t, expected, Dir())
			})
		})
	}
}

func TestLogDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // For Windows
			expected := map[string]string{
				"windows": filepath.Join(home, "AppData", "Local", "rancher-desktop-2-logs"),
				"linux":   filepath.Join(home, ".local", "state", "rancher-desktop-2"),
				"darwin":  filepath.Join(home, "Library", "Logs", "rancher-desktop-2"),
			}[runtime.GOOS]
			assert.Equal(t, expected, LogDir())
		})
	})

	t.Run("override", func(t *testing.T) {
		runTestProcess(t, func(t *testing.T) {
			expected := t.TempDir()
			t.Setenv("RDD_LOG_DIR", expected)
			assert.Equal(t, expected, LogDir())
		})
	})
}

func TestDockerEndpoint(t *testing.T) {
	runTestProcess(t, func(t *testing.T) {
		if runtime.GOOS == "windows" {
			assert.Equal(t, "npipe:////./pipe/docker_engine", DockerEndpoint())
		} else {
			assert.Equal(t, fmt.Sprintf("unix://%s/docker.sock", ShortDir()), DockerEndpoint())
		}
	})
}

func TestContainerdSocket(t *testing.T) {
	runTestProcess(t, func(t *testing.T) {
		if runtime.GOOS == "windows" {
			assert.Equal(t, `\\.\pipe\containerd-rancher-desktop-test`, ContainerdSocket())
		} else {
			assert.Equal(t, filepath.Join(ShortDir(), "containerd.sock"), ContainerdSocket())
		}
	}, "RDD_INSTANCE=test")
}

func TestValidateSuffix(t *testing.T) {
	for _, suffix := range []string{"2", "0", "99", "bats-cli", "a", "logs"} {
		t.Run("valid "+suffix, func(t *testing.T) {
			assert.NilError(t, ValidateSuffix(suffix))
		})
	}
	for _, suffix := range []string{"", "DEV", "Dev", "-x", "1.2", "a/b", "a b", `a\b`, "a_b", "2-logs", "bats-logs"} {
		t.Run("invalid "+suffix, func(t *testing.T) {
			assert.ErrorContains(t, ValidateSuffix(suffix), fmt.Sprintf("invalid instance name %q", suffix))
		})
	}
}
