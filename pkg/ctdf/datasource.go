package ctdf

type DataSource struct {
	OriginalFormat string `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	Provider       string `groups:"internal"`
	DatasetID      string `groups:"internal"`
	Timestamp      string `groups:"internal"`
}
