// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// Command go-module-k8s-bump is used to bump Kubernetes versions in the
// top-level `go.mod` because we require `replace` directives to make various
// Kubernetes components match.
//
// This must be run with internet access, and may modify `go.mod` and `go.sum`.
//
// See `.github/workflows/go-mod-k8s-sync.yaml` for where this is run on CI.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"sigs.k8s.io/yaml"
)

const (
	// The name of the main Kubernetes package.
	anchorPackage = "k8s.io/kubernetes"
	// Path to dependabot configuration. The script runs from rdd/, so reach
	// up to the repository-root .github/.
	dependabotConfigPath = "../.github/dependabot.yml"
)

// Run the command, setting up stdout/stderr.
func runCommand(ctx context.Context, capture bool, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = os.Stderr
	if capture {
		cmd.Stdout = &buf
	} else {
		cmd.Stdout = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Download the k8s.io/kubernetes package, and determine which packages are found
// in its staging/ directory.
func findModules(ctx context.Context, mod module.Version) (map[string]bool, error) {
	// The canonical list of packages is found in the Kubernetes source tree,
	// under `staging/src/...`; however, there is no good way to find that.  We
	// make do by checking the `go.mod` of the main `k8s.io/kubernetes` package.
	// To do so, though, we need to `go get` the specific version first.
	// Note that `go get` does _not_ end up with the `staging/src/...` files.
	_, err := runCommand(ctx, false, "go", "get", mod.String())
	if err != nil {
		return nil, err
	}
	modFilePath, err := runCommand(ctx, true, "go", "list", "-m", "-f", "{{.GoMod}}", mod.Path)
	if err != nil {
		return nil, err
	}
	modFilePath = strings.TrimSpace(modFilePath)
	if modFilePath == "" {
		return nil, errors.New("failed to find Kubernetes go.mod")
	}
	data, err := os.ReadFile(modFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read Kubernetes go.mod: %w", err)
	}
	// `modfile.ParseLax` ignores `replace` directives, so we need to use `Parse`.
	file, err := modfile.Parse(modFilePath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Kubernetes go.mod: %w", err)
	}

	results := make(map[string]bool)
	for _, replace := range file.Replace {
		if strings.HasPrefix(replace.Old.Path, "k8s.io/") {
			results[replace.Old.Path] = true
		}
	}
	return results, nil
}

// Update the go.mod file in the working directory such that all k8s.io modules
// use the same version as the anchor module (using v0 instead of v1).
func updateGoMod(ctx context.Context) error {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return err
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}

	i := slices.IndexFunc(file.Require, func(r *modfile.Require) bool {
		return r.Mod.Path == anchorPackage
	})
	if i < 0 {
		return fmt.Errorf("failed to find module %s in go.mod", anchorPackage)
	}
	anchorRequire := file.Require[i]
	targetVersion := strings.Replace(anchorRequire.Mod.Version, "v1.", "v0.", 1)
	targetModules, err := findModules(ctx, anchorRequire.Mod)
	if err != nil {
		return fmt.Errorf("failed to find Kubernetes sub-packages: %w", err)
	}

	// Just always add everything; we run `go mod tidy` after, and that will
	// remove things we added that are not necessary.
	for pkg := range targetModules {
		if err := file.AddRequire(pkg, targetVersion); err != nil {
			return fmt.Errorf("error updating require %s: %w", pkg, err)
		}
		if err := file.AddReplace(pkg, "", pkg, targetVersion); err != nil {
			return fmt.Errorf("error updating replace: %s => %s: %w", pkg, pkg, err)
		}
	}

	file.Cleanup()

	result, err := file.Format()
	if err != nil {
		return err
	}

	if err := os.WriteFile("go.mod", result, 0o644); err != nil {
		return err
	}

	if _, err := runCommand(ctx, false, "go", "mod", "tidy"); err != nil {
		return err
	}

	if err := checkDependabotConfig(targetModules); err != nil {
		return err
	}

	return nil
}

// Check the dependabot configuration to make sure it is ignoring all of the
// Kubernetes modules correctly; print out a list of missing modules and return
// an error if any are missing.  We do not modify the file in-place because
// doing so would end up dropping any comments in the file.
func checkDependabotConfig(modules map[string]bool) error {
	buf, err := os.ReadFile(dependabotConfigPath)
	if err != nil {
		return fmt.Errorf("error reading dependabot configuration: %w", err)
	}
	var config struct {
		Updates []struct {
			PackageEcosystem string `json:"package-ecosystem"`
			Directory        string `json:"directory"`
			Ignore           []struct {
				DependencyName string `json:"dependency-name"`
			}
		}
	}
	if err := yaml.Unmarshal(buf, &config); err != nil {
		return fmt.Errorf("failed to unmarshal dependabot configuration: %w", err)
	}
	for _, updates := range config.Updates {
		if updates.PackageEcosystem != "gomod" {
			continue
		}
		if updates.Directory != "/rdd" {
			continue
		}
		found := make(map[string]bool)
		for _, dependency := range updates.Ignore {
			found[dependency.DependencyName] = true
		}
		var missing []string
		for module := range modules {
			if _, ok := found[module]; !ok {
				missing = append(missing, module)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		slices.Sort(missing)
		fmt.Fprintf(os.Stderr, "Dependabot configuration is missing ignore packages:\n")
		for _, module := range missing {
			fmt.Fprintf(os.Stderr, "  - { dependency-name: %s }\n", module)
		}
		return errors.New("dependabot configuration is not ignoring Kubernetes packages")
	}
	return errors.New("failed to find dependabot golang configuration")
}

func main() {
	if err := updateGoMod(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
