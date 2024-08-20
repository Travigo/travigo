package ctdf

import "time"

type DatasetVersion struct {
	Dataset      string `gorm:"uniqueIndex"`
	Hash         string
	LastModified time.Time
}
