package ctdf

import "time"

type ServiceAlert struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	AlertType ServiceAlertType `groups:"basic"`

	Text string `groups:"basic"`

	MatchedIdentifiers []string `groups:"internal"`

	ValidFrom  time.Time `groups:"internal"`
	ValidUntil time.Time `groups:"internal"`
}

type ServiceAlertType string

//goland:noinspection GoUnusedConst
const (
	ServiceAlertTypeInformation      ServiceAlertType = "Information"
	ServiceAlertTypeWarning                           = "Warning"
	ServiceAlertTypeStopClosed                        = "StopClosed"
	ServiceAlertTypeServiceSuspended                  = "ServiceSuspended"
	ServiceAlertTypeDisruption                        = "Disruption"
	ServiceAlertTypePlanned                           = "Planned"
)

func (a *ServiceAlert) IsValid(checkTime time.Time) bool {
	return checkTime.After(a.ValidFrom) && checkTime.Before(a.ValidUntil)
}
