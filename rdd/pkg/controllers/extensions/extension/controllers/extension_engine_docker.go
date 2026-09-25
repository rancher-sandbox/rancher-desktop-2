// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const containerTemporaryLabel = "extensions.rancherdesktop.io/temporary"

type dockerEngine struct {
	cli atomic.Pointer[mobyclient.Client]
}

var _ engine = &dockerEngine{}

// connect implements [engine].
func (d *dockerEngine) connect(ctx context.Context) error {
	cli, err := mobyclient.New(mobyclient.WithHost(instance.DockerEndpoint()))
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify the connection by pinging Docker.
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	_, err = cli.Ping(pingCtx, mobyclient.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		cli.Close()
		return fmt.Errorf("failed to ping Docker: %w", err)
	}

	d.cli.Store(cli)
	return nil
}

// createForExport implements [engine]. It creates a temporary container for exporting files from the extension image.
func (d *dockerEngine) createForExport(ctx context.Context, ext *v1alpha1.Extension) (engineCreateForExportResult, error) {
	cli := d.cli.Load()
	if cli == nil {
		return engineCreateForExportResult{}, errors.New("docker client is not connected")
	}

	image := ext.Status.Image
	if image == "" {
		return engineCreateForExportResult{}, fmt.Errorf("extension %q image is not resolved", ext.Name)
	}

	create, err := cli.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{
		Config: &mobycontainer.Config{
			Image: image,
			Labels: map[string]string{
				containerTemporaryLabel: "true",
			},
		},
	})
	if err != nil {
		return engineCreateForExportResult{}, fmt.Errorf("failed to create container: %w", err)
	}
	cleanup := func() {
		_, _ = cli.ContainerRemove(ctx, create.ID, mobyclient.ContainerRemoveOptions{Force: true})
	}
	return engineCreateForExportResult{
		id:      create.ID,
		cleanup: cleanup,
	}, nil
}

// export implements [engine].
func (d *dockerEngine) export(ctx context.Context, opts engineExportOptions) error {
	cli := d.cli.Load()

	if opts.ch == nil {
		return errors.New("invalid options")
	}

	if cli == nil {
		return errors.New("docker client is not connected")
	}

	emit := func(progress result[engineExportProgress]) error {
		select {
		case opts.ch <- progress:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	go func() {
		defer close(opts.ch)
		err := func() error {
			if err := emit(success(engineExportProgressStarted)); err != nil {
				return err
			}

			c, err := cli.CopyFromContainer(ctx, opts.id, mobyclient.CopyFromContainerOptions{
				SourcePath: opts.sourcePath,
			})
			if err != nil {
				return fmt.Errorf("failed to copy from container: %w", err)
			}
			defer c.Content.Close()
			if err := emit(success(engineExportProgressCopying)); err != nil {
				return err
			}

			if c.Stat.Mode.IsDir() != opts.isDirectory {
				return fmt.Errorf("source path type mismatch: expected directory=%v, got %s",
					opts.isDirectory, c.Stat.Mode)
			}

			root, err := os.OpenRoot(opts.destDir)
			if err != nil {
				return fmt.Errorf("failed to open destination directory %q: %w", opts.destDir, err)
			}
			defer root.Close()
			reader := tar.NewReader(c.Content)
			for {
				header, err := reader.Next()
				if err != nil {
					if err == io.EOF {
						break
					}
					return fmt.Errorf("failed to read tar archive: %w", err)
				}
				relPath := strings.TrimPrefix(path.Clean(filepath.ToSlash(header.Name)), "/")
				if !filepath.IsLocal(relPath) {
					return fmt.Errorf("invalid relative path %q in tar archive", relPath)
				}

				switch header.Typeflag {
				case tar.TypeDir:
					parent := path.Dir(relPath)
					if parent != "" {
						if err := root.MkdirAll(parent, 0o755); err != nil {
							return fmt.Errorf("failed to create directory %q: %w", relPath, err)
						}
					}
					err := root.Mkdir(relPath, header.FileInfo().Mode())
					if errors.Is(err, os.ErrExist) {
						err = root.Chmod(relPath, header.FileInfo().Mode())
					}
					if err != nil {
						return fmt.Errorf("failed to create directory %q: %w", relPath, err)
					}
				case tar.TypeReg:
					// Regular file; process as needed.
					file, err := root.OpenFile(
						relPath,
						os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
						header.FileInfo().Mode())
					if err != nil {
						return fmt.Errorf("failed to create file %q: %w", relPath, err)
					}
					if _, err := io.Copy(file, reader); err != nil {
						_ = file.Close()
						return fmt.Errorf("failed to write file %q: %w", relPath, err)
					}
					if err := file.Close(); err != nil {
						return fmt.Errorf("failed to close file %q: %w", relPath, err)
					}
				default:
					continue
				}
			}

			return nil
		}()
		if err != nil {
			_ = emit(failure[engineExportProgress](err))
		} else {
			_ = emit(success(engineExportProgressCompleted))
		}
	}()

	return nil
}
