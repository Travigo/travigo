package datasets

type SupportedObjects struct {
	Operators      bool
	OperatorGroups bool
	Stops          bool
	StopGroups     bool
	Services       bool
	Journeys       bool

	RealtimeJourneys bool
	ServiceAlerts    bool
}
