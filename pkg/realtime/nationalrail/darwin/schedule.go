package darwin

type Schedule struct {
	RID string `xml:"rid,attr"`
	UID string `xml:"uid,attr"`
	SSD string `xml:"ssd,attr"`
	TOC string `xml:"toc,attr"`

	CancelReason string `xml:"cancelReason"`

	Origin       ScheduleStop   `xml:"OR"`
	Intermediate []ScheduleStop `xml:"IP"`
	Destination  ScheduleStop   `xml:"DT"`
}

type ScheduleStop struct {
	Tiploc   string `xml:"tpl,attr"`
	Activity string `xml:"act,attr"`

	PublicDeparture  string `xml:"ptd,attr"`
	WorkingDeparture string `xml:"wtd,attr"`

	PublicArrival  string `xml:"pta,attr"`
	WorkingArrival string `xml:"wta,attr"`

	Cancelled string `xml:"can,attr"`
}
