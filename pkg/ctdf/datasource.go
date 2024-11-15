package ctdf

type DataSource struct {
	OriginalFormat string `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	Provider       string `groups:"detailed"`
	DatasetID      string `groups:"detailed"`
	Timestamp      string `groups:"internal"`
}
