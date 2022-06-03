package realtime

import (
	"io/fs"
	"time"
)

type MemoryFileInfo struct {
	MFI_Name    string
	MFI_Size    int64
	MFI_Mode    fs.FileMode
	MFI_ModTime time.Time
	MFI_IsDir   bool
}

func (m MemoryFileInfo) Name() string {
	return m.MFI_Name
}

func (m MemoryFileInfo) Size() int64 {
	return m.MFI_Size
}

func (m MemoryFileInfo) Mode() fs.FileMode {
	return m.MFI_Mode
}

func (m MemoryFileInfo) ModTime() time.Time {
	return m.MFI_ModTime
}

func (m MemoryFileInfo) IsDir() bool {
	return m.MFI_IsDir
}

func (m MemoryFileInfo) Sys() any {
	return nil
}
