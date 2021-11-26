package naptan

type StopArea struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode          string
	Name                  string
	AdministrativeAreaRef string
	StopAreaType          string

	Location *Location
}
