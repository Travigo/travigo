//go:build linux

package departuregraph

import (
	"os"

	"golang.org/x/sys/unix"
)

const snapshotCheckpointSyncBytes = int64(64 * 1024 * 1024)

type linuxSnapshotCheckpointWriter struct {
	file           *os.File
	written        int64
	evictedThrough int64
}

func newSnapshotCheckpointWriter(file *os.File) snapshotCheckpointWriter {
	return &linuxSnapshotCheckpointWriter{file: file}
}

func (w *linuxSnapshotCheckpointWriter) Write(value []byte) (int, error) {
	written, err := w.file.Write(value)
	w.written += int64(written)
	if err != nil || w.written-w.evictedThrough < snapshotCheckpointSyncBytes {
		return written, err
	}
	if err := w.flushWrittenPages(); err != nil {
		return written, err
	}
	return written, nil
}

func (w *linuxSnapshotCheckpointWriter) Finalize() error {
	return w.flushWrittenPages()
}

func (w *linuxSnapshotCheckpointWriter) flushWrittenPages() error {
	if err := w.file.Sync(); err != nil {
		return err
	}
	length := w.written - w.evictedThrough
	if length > 0 {
		if err := unix.Fadvise(int(w.file.Fd()), w.evictedThrough, length, unix.FADV_DONTNEED); err != nil {
			return err
		}
		w.evictedThrough = w.written
	}
	return nil
}
