package darwin

import (
	"errors"
	"time"
)

type TrainStatus struct {
	RID string `xml:"rid,attr"`
	UID string `xml:"uid,attr"`
	SSD string `xml:"ssd,attr"`

	LateReason string

	Locations []TrainStatusLocation `xml:"Location"`
}

type TrainStatusLocation struct {
	TPL string `xml:"tpl,attr"`
	WTD string `xml:"wtd,attr"`
	PTD string `xml:"ptd,attr"`

	Departure *TrainStatusTiming   `xml:"dep"`
	Arrival   *TrainStatusTiming   `xml:"arr"`
	Pass      *TrainStatusTiming   `xml:"pass"`
	Platform  *TrainStatusPlatform `xml:"plat"`
}

type TrainStatusTiming struct {
	AT    string `xml:"at,attr"`
	ATMIN string `xml:"atmin,attr"`

	ET    string `xml:"et,attr"`
	ETMIN string `xml:"etmin,attr"`

	SRC     string `xml:"src,attr"`
	SRCINST string `xml:"srcInst,attr"`

	Delayed string `xml:"delayed,attr"`
}

func (t *TrainStatusTiming) GetTiming(referenceDate time.Time) (time.Time, error) {
	var statusTimeString string

	if t.ET != "" {
		statusTimeString = t.ET
	}
	if t.ETMIN != "" {
		statusTimeString = t.ETMIN
	}

	if t.AT != "" {
		statusTimeString = t.AT
	}
	if t.ATMIN != "" {
		statusTimeString = t.ATMIN
	}

	if statusTimeString == "" {
		return time.Time{}, errors.New("Cannot find time")
	}

	statusTime, err := time.Parse("15:04", statusTimeString)

	statusDateTime := time.Date(
		referenceDate.Year(), referenceDate.Month(), referenceDate.Day(),
		statusTime.Hour(), statusTime.Minute(), statusTime.Second(), statusTime.Nanosecond(), referenceDate.Location(),
	)

	return statusDateTime, err
}

type TrainStatusPlatform struct {
	PLATSRC    string `xml:"platsrc"`
	PLATSUP    string `xml:"platsup"`
	CISPLATSUP string `xml:"cisPlatsup"`
	CONF       string `xml:"conf"`

	Name string `xml:",chardata"`
}
