package ctdf

type DataSource struct {
	OriginalFormat string // or enum (eg. NaPTAN, TransXChange)
	Provider       string
	Dataset        string
	Identifier     string
}
