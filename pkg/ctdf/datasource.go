package ctdf

type DataSourceReference struct {
	OriginalFormat string `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	ProviderName   string `groups:"detailed,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	ProviderID     string `groups:"detailed,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	DatasetID      string `groups:"detailed,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	Timestamp      string `groups:"internal"`
}
