package datasets

type DataSource struct {
	Identifier string
	Region     string
	Provider   Provider
	Datasets   []DataSet

	SourceAuthentication *SourceAuthentication
}
