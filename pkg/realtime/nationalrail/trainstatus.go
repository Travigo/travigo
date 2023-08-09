package nationalrail

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

	Departure TrainStatusTiming   `xml:"dep"`
	Arrival   TrainStatusTiming   `xml:"arr"`
	Pass      TrainStatusTiming   `xml:"pass"`
	Platform  TrainStatusPlatform `xml:"plat"`
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

type TrainStatusPlatform struct {
	PLATSRC    string `xml:"platsrc"`
	PLATSUP    string `xml:"platsup"`
	CISPLATSUP string `xml:"cisPlatsup"`
	CONF       string `xml:"conf"`

	Name string `xml:",chardata"`
}
