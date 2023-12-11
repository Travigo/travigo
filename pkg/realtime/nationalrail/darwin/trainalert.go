package darwin

type TrainAlert struct {
	AlertID string

	SendAlertBySMS     bool
	SendAlertByEmail   bool
	SendAlertByTwitter bool

	AlertServices []TrainAlertServices `xml:"AlertServices>AlertService"`

	Source string

	AlertText string

	Audience  string
	AlertType string
}

type TrainAlertServices struct {
	RID string `xml:",attr"`
	UID string `xml:",attr"`
	SSD string `xml:",attr"`

	Locations []string `xml:"Location"`
}
