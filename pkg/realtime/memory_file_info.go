package realtime

import (
	"io/fs"
	"time"
)

type MemoryFileInfo struct {
	MfiName    string
	MfiSize    int64
	MfiMode    fs.FileMode
	MfiModTime time.Time
	MfiIsDir   bool
}

func (m MemoryFileInfo) Name() string {
	return m.MfiName
}

func (m MemoryFileInfo) Size() int64 {
	return m.MfiSize
}

func (m MemoryFileInfo) Mode() fs.FileMode {
	return m.MfiMode
}

func (m MemoryFileInfo) ModTime() time.Time {
	return m.MfiModTime
}

func (m MemoryFileInfo) IsDir() bool {
	return m.MfiIsDir
}

func (m MemoryFileInfo) Sys() any {
	return nil
}
