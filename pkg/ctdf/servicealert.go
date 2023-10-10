package ctdf

import "time"

type ServiceAlert struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	AlertType ServiceAlertType `groups:"basic"`

	Title string `groups:"basic"`
	Text  string `groups:"basic"`

	MatchedStops    []string  `groups:"internal"`
	MatchedServices []string  `groups:"internal"`
	ValidFrom       time.Time `groups:"internal"`
	ValidUntil      time.Time `groups:"internal"`
}

type ServiceAlertType string

//goland:noinspection GoUnusedConst
const (
	ServiceAlertTypeInformation      ServiceAlertType = "Information"
	ServiceAlertTypeWarning                           = "Warning"
	ServiceAlertTypeStopClosed                        = "StopClosed"
	ServiceAlertTypeServiceSuspended                  = "ServiceSuspended"
	ServiceAlertTypeDisruption                        = "Disruption"
)
