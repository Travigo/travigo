//go:build !linux

package departuregraph

import "os"

func newSnapshotCheckpointWriter(file *os.File) snapshotCheckpointWriter {
	return &standardSnapshotCheckpointWriter{file: file}
}
