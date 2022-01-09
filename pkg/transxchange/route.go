package transxchange

type Route struct {
	ID                   string `xml:"id,attr"`
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	PrivateCode     string
	Description     string
	RouteSectionRef []string
}
