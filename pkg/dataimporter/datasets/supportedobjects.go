package datasets

type SupportedObjects struct {
	Operators      bool `groups:"web-datasource"`
	OperatorGroups bool `groups:"web-datasource"`
	Stops          bool `groups:"web-datasource"`
	StopGroups     bool `groups:"web-datasource"`
	StopsDetailed  bool `groups:"web-datasource"`
	Services       bool `groups:"web-datasource"`
	Journeys       bool `groups:"web-datasource"`
	JourneyTracks  bool `groups:"web-datasource"`

	RealtimeJourneys bool `groups:"web-datasource"`
	ServiceAlerts    bool `groups:"web-datasource"`
}
