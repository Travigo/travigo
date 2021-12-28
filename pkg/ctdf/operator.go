package ctdf

type Operator struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	PrimaryName string
	OtherNames  []string

	OperatorGroup string

	TransportType []string

	Licence string

	// LegalEntity string
	Website     string
	Email       string
	Address     string
	PhoneNumber string
	SocialMedia map[string]string
}
