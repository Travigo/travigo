package ctdf

type Stop struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	PrimaryName string
	OtherNames  map[string]string
	Type        string // or an enum
	Status      string

	Location *Location

	Associations []StopAssociation
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
