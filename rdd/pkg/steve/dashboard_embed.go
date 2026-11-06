// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build with_dashboard

package steve

import (
	"embed"
	"io/fs"
)

//go:embed dashboard/*
var embeddedDashboard embed.FS

func init() {
	files, err := fs.Sub(embeddedDashboard, "dashboard")
	if err != nil {
		panic(err)
	}
	dashboardFiles = files
}
