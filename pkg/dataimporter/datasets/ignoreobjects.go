package datasets

type IgnoreObjects struct {
	// Operators      bool
	// OperatorGroups bool
	// Stops          bool
	// StopGroups     bool
	Services IgnoreObjectServiceJourney
	Journeys IgnoreObjectServiceJourney

	// RealtimeJourneys bool
	// ServiceAlerts    bool
}

type IgnoreObjectServiceJourney struct {
	ByOperator []string
}
