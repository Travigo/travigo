package datasets

type DataSource struct {
	Identifier string    `groups:"web-datasource"`
	Region     string    `groups:"web-datasource"`
	Provider   Provider  `groups:"web-datasource"`
	Datasets   []DataSet `groups:"web-datasource"`

	SourceAuthentication *SourceAuthentication `json:"-"`
}
