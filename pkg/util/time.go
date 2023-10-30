package util

import (
	"time"
)

func AddTimeToDate(date time.Time, sourceTime time.Time) time.Time {
	newDateTime := time.Date(date.Year(), date.Month(), date.Day(), sourceTime.Hour(), sourceTime.Minute(), sourceTime.Second(), sourceTime.Nanosecond(), date.Location())

	return newDateTime
}
