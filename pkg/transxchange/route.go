package transxchange

type Route struct {
	ID                   string `xml:",attr"`
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	PrivateCode     string
	Description     string
	RouteSectionRef string
}
