// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"log"
	"os"
	"os/exec"
)

func main() {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, os.Args[1], os.Args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if exitError, ok := err.(*exec.ExitError); ok {
		if exitError.ExitCode() > -1 {
			os.Exit(exitError.ExitCode())
		}
	}
	if err != nil {
		log.Fatal(err)
	}
}
