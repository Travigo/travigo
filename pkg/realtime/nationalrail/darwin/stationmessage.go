package darwin

type StationMessage struct {
	ID       string `xml:"id,attr"`
	Category string `xml:"cat,attr"`
	Severity string `xml:"sev,attr"`

	Stations []StationMessageStation `xml:"Station"`
	Message  string                  `xml:"Msg,innerxml"`
}

type StationMessageStation struct {
	CRS string `xml:"crs,attr"`
}
