package ctdf

type DataSource struct {
	OriginalFormat string `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	Provider       string `groups:"internal"`
	Dataset        string `groups:"internal"`
	Identifier     string `groups:"internal"`
}
