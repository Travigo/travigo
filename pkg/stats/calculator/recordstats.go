package calculator

import "time"

type RecordStatsData struct {
	Type      string `json:"-"`
	Stats     interface{}
	Timestamp time.Time
}
