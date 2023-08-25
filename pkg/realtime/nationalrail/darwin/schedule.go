package darwin

type Schedule struct {
	RID string `xml:"rid,attr"`
	UID string `xml:"uid,attr"`
	SSD string `xml:"ssd,attr"`

	CancelReason string `xml:"cancelReason"`
}
