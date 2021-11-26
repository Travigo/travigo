package naptan

type StopPoint struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	AtcoCode              string
	NaptanCode            string
	AdministrativeAreaRef string

	Descriptor *StopPointDescriptor

	NptgLocalityRef string    `xml:"Place>NptgLocalityRef"`
	LocalityCentre  bool      `xml:"Place>LocalityCentre"`
	Location        *Location `xml:"Place>Location"`

	StopType       string `xml:"StopClassification>StopType"`
	BusStopType    string `xml:"StopClassification>OnStreet>Bus>BusStopType"`
	BusStopBearing string `xml:"StopClassification>OnStreet>Bus>MarkedPoint>Bearing>CompassPoint"`

	StopAreas []StopPointStopAreaRef `xml:"StopAreas>StopAreaRef"`
}

type StopPointDescriptor struct {
	CommonName      string
	ShortCommonName string
	Landmark        string
	Street          string
	Indicator       string
}

type StopPointStopAreaRef struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode string `xml:",chardata"`
}
