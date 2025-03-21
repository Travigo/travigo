package ctdf

import "time"

type ServiceAlert struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSourceReference `groups:"internal"`

	AlertType ServiceAlertType `groups:"basic"`

	Title string `groups:"basic"`
	Text  string `groups:"basic"`

	MatchedIdentifiers []string `groups:"internal"`

	ValidFrom  time.Time `groups:"internal"`
	ValidUntil time.Time `groups:"internal"`
}

type ServiceAlertType string

const (
	ServiceAlertTypeInformation               ServiceAlertType = "Information"
	ServiceAlertTypeWarning                                    = "Warning"
	ServiceAlertTypeStopClosed                                 = "StopClosed"
	ServiceAlertTypeServiceSuspended                           = "ServiceSuspended"
	ServiceAlertTypeServicePartSuspended                       = "ServicePartSuspended"
	ServiceAlertTypeSevereDelays                               = "SevereDelays"
	ServiceAlertTypeDelays                                     = "Delays"
	ServiceAlertTypeMinorDelays                                = "MinorDelays"
	ServiceAlertTypePlanned                                    = "Planned"
	ServiceAlertTypeJourneyDelayed                             = "JourneyDelayed"
	ServiceAlertTypeJourneyPartiallyCancelled                  = "JourneyPartiallyCancelled"
	ServiceAlertTypeJourneyCancelled                           = "JourneyCancelled"
)

func (a *ServiceAlert) IsValid(checkTime time.Time) bool {
	return checkTime.After(a.ValidFrom) && checkTime.Before(a.ValidUntil)
}
