package ctdf

type DataSourceReference struct {
	OriginalFormat string `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	ProviderName   string `groups:"detailed"`
	ProviderID     string `groups:"detailed"`
	DatasetID      string `groups:"detailed"`
	Timestamp      string `groups:"internal"`
}
