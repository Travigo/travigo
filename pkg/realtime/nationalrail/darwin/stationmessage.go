package darwin

type StationMessage struct {
	ID       string `xml:"id,attr"`
	Category string `xml:"cat,attr"`
	Severity string `xml:"sev,attr"`

	Stations []StationMessageStation `xml:"Station"`
	Message  StationMessageMessage   `xml:"Msg"`
}

type StationMessageStation struct {
	CRS string `xml:"crs,attr"`
}

type StationMessageMessage struct {
	InnerXML string `xml:",innerxml"`
}
