package ctdf

type LinkableRecord interface {
	GenerateDeterministicID() string
}

type BaseRecord struct {
	PrimaryIdentifier string
	OtherIdentifiers  []string
}
