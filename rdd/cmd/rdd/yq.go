// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-FileCopyrightText: Copyright (c) 2017 Mike Farah

// This file has been adapted from https://github.com/lima-vm/lima/blob/master/cmd/yq/yq.go

package main

import (
	"os"

	command "github.com/mikefarah/yq/v4/cmd"
	"github.com/spf13/cobra"
)

func newYQCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "yq",
		Short:              "Evaluate and filter YAML documents",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               yqAction,
	}
}

func yqAction(*cobra.Command, []string) error {
	os.Args = os.Args[1:]
	cmd := command.New()
	args := os.Args[1:]
	if len(args) > 0 {
		_, _, err := cmd.Find(args)
		if err != nil && args[0] != "__complete" {
			newArgs := []string{"eval"}
			cmd.SetArgs(append(newArgs, os.Args[1:]...))
		}
	}
	if err := cmd.Execute(); err != nil {
		os.Exit(1) //nolint:revive // yq already printed the error; returning it would double-print via logrus.Fatal
	}
	return nil
}
