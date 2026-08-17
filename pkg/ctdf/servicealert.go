package ctdf

import "time"

type ServiceAlert struct {
	PrimaryIdentifier string            `groups:"basic,web-alert,web-alert-matching"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed,web-alert,web-alert-matching"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSourceReference `groups:"internal"`

	AlertType ServiceAlertType `groups:"basic,web-alert,web-alert-matching"`

	Title string `groups:"basic,web-alert,web-alert-matching"`
	Text  string `groups:"basic,web-alert,web-alert-matching"`

	MatchedIdentifiers []string `groups:"internal,web-alert-matching"`

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

func AllServiceAlertTypes() []string {
	return []string{
		string(ServiceAlertTypeInformation), string(ServiceAlertTypeWarning), string(ServiceAlertTypeStopClosed),
		string(ServiceAlertTypeServiceSuspended), string(ServiceAlertTypeServicePartSuspended), string(ServiceAlertTypeSevereDelays),
		string(ServiceAlertTypeDelays), string(ServiceAlertTypeMinorDelays), string(ServiceAlertTypePlanned),
		string(ServiceAlertTypeJourneyDelayed), string(ServiceAlertTypeJourneyPartiallyCancelled), string(ServiceAlertTypeJourneyCancelled),
	}
}

func (a *ServiceAlert) IsValid(checkTime time.Time) bool {
	return checkTime.After(a.ValidFrom) && checkTime.Before(a.ValidUntil)
}
