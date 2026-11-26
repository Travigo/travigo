package ctdf

import "context"

type DataSourceReference struct {
	OriginalFormat      string          `groups:"internal"` // or enum (eg. NaPTAN, TransXChange)
	ProviderName        string          `groups:"detailed,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	ProviderID          string          `groups:"detailed,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	DatasetID           string          `groups:"detailed,departureboard-cache,web-stop-detail,web-stop-detailed,web-journey,web-service-detail,web-operator-detail"`
	Timestamp           string          `groups:"internal"`
	FareVersionID       string          `groups:"internal" bson:",omitempty" json:"-"`
	FareSourceSequence  string          `groups:"internal" bson:",omitempty" json:"-"`
	ImportContext       context.Context `groups:"internal" bson:"-" json:"-"`
	FareLeaseOwner      string          `groups:"internal" bson:"-" json:"-"`
	FareActivationFence int64           `groups:"internal" bson:"-" json:"-"`
}

func (reference *DataSourceReference) GetImportContext() context.Context {
	if reference != nil && reference.ImportContext != nil {
		return reference.ImportContext
	}
	return context.Background()
}
