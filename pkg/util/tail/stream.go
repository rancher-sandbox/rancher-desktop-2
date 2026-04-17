// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package tail

import (
	"context"
	"fmt"
	"io"

	"github.com/sirupsen/logrus"
)

// Stream prints the contents of filePath to writer. If follow is true, it waits
// for new lines at EOF; otherwise it returns at EOF. The stream stops when ctx
// is cancelled or (when follow is false) EOF is reached.
func Stream(ctx context.Context, writer io.Writer, filePath string, follow bool) error {
	config := Config{
		ReOpen:        follow,
		Follow:        follow,
		CompleteLines: true,
		Logger:        logrus.StandardLogger(),
	}
	t, err := Open(filePath, config)
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
