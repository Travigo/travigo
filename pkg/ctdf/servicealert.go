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
	ServiceAlertTypeWarning                   ServiceAlertType = "Warning"
	ServiceAlertTypeStopClosed                ServiceAlertType = "StopClosed"
	ServiceAlertTypeServiceSuspended          ServiceAlertType = "ServiceSuspended"
	ServiceAlertTypeServicePartSuspended      ServiceAlertType = "ServicePartSuspended"
	ServiceAlertTypeSevereDelays              ServiceAlertType = "SevereDelays"
	ServiceAlertTypeDelays                    ServiceAlertType = "Delays"
	ServiceAlertTypeMinorDelays               ServiceAlertType = "MinorDelays"
	ServiceAlertTypePlanned                   ServiceAlertType = "Planned"
	ServiceAlertTypeJourneyDelayed            ServiceAlertType = "JourneyDelayed"
	ServiceAlertTypeJourneyPartiallyCancelled ServiceAlertType = "JourneyPartiallyCancelled"
	ServiceAlertTypeJourneyCancelled          ServiceAlertType = "JourneyCancelled"
)

func (a *ServiceAlert) IsValid(checkTime time.Time) bool {
	return checkTime.After(a.ValidFrom) && checkTime.Before(a.ValidUntil)
}
