package datalinker

type Linker interface {
	GetBaseCollectionName() string
	Run()
}

type BaseRecord struct {
	PrimaryIdentifier string
	OtherIdentifiers  []string
}
