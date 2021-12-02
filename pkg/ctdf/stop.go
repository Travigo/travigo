package ctdf

type Stop struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	PrimaryName string
	OtherNames  map[string]string
	Type        string // or an enum
	Status      string
	DataSource  *DataSource

	Location *Location

	Associations []StopAssociation
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
