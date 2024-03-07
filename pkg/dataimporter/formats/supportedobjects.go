package formats

type SupportedObjects struct {
	Operators bool
	Stops     bool
	Services  bool
	Journeys  bool

	RealtimeJourneys bool
	ServiceAlerts    bool
}
