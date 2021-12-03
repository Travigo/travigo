package ctdf

type Operator struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	PrimaryName string
	OtherNames  map[string]string

	LegalEntity string
	Website     string
	Address     string
	SocialMedia map[string]string
}
