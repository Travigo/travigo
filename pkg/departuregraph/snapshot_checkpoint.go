package departuregraph

import (
	"io"
	"os"
)

type snapshotCheckpointWriter interface {
	io.Writer
	Finalize() error
}

type standardSnapshotCheckpointWriter struct {
	file *os.File
}

func (w *standardSnapshotCheckpointWriter) Write(value []byte) (int, error) {
	return w.file.Write(value)
}

func (w *standardSnapshotCheckpointWriter) Finalize() error {
	return w.file.Sync()
}
