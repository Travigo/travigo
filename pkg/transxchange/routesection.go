package transxchange

type RouteSection struct {
	ID string `xml:"id,attr"`

	RouteLinks []RouteLink `xml:"RouteLink"`
}

type RouteLink struct {
	ID                   string `xml:"id,attr"`
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	FromStop string `xml:"From>StopPointRef"`
	ToStop   string `xml:"To>StopPointRef"`
	Distance int

	Track []Location `xml:"Track>Mapping>Location"`
}
