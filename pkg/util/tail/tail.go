// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package tail

import (
	"context"
	"fmt"
	"io"

	"github.com/sirupsen/logrus"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/nxadmtail"
)

// File prints the contents of filePath to writer. If follow is true, it waits
// for new lines at EOF; otherwise it returns at EOF.
func File(ctx context.Context, writer io.Writer, filePath string, follow bool) error {
	config := nxadmtail.Config{
		ReOpen:        follow,
		Follow:        follow,
		CompleteLines: true,
		Logger:        logrus.StandardLogger(),
	}
	t, err := nxadmtail.File(filePath, config)
	if err != nil {
		return err
	}

	if !follow {
		// Signal that we want to stop tailing at EOF.
		go func() {
			_ = t.StopAtEOF()
		}()
	}
	go func() {
		<-ctx.Done()
		_ = t.Stop()
	}()

	for line := range t.Lines {
		fmt.Fprintln(writer, line.Text)
	}
	return nil
}
