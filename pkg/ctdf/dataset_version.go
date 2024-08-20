package ctdf

import "time"

type DatasetVersion struct {
	Dataset      string
	Hash         string
	LastModified time.Time
}
