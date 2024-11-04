package ctdf

import "time"

type DatasetVersion struct {
	Dataset      string
	Hash         string
	ETag         string
	LastModified time.Time
}
