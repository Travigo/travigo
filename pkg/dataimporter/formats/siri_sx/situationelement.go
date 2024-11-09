package siri_sx

type SituationElement struct {
	CreationTime    string
	ParticipantRef  string
	SituationNumber string
	Version         string
	VersionedAtTime string
	Progress        string

	ValidityPeriod    TimePeriod
	PublicationWindow TimePeriod

	MiscellaneousReason string
	Planned             bool
	Summary             string
	Description         string
	InfoURL             string `xml:"InfoLinks>InfoLink>Uri"`

	Consequence []Consequence `xml:"Consequences>Consequence"`
}

type TimePeriod struct {
	StartTime string
	EndTime   string
}

type Consequence struct {
	Condition string
	Severity  string

	BlockingJourneyPlanner bool `xml:"Blocking>JourneyPlanner"`

	AffectedNetworks   []AffectedNetwork   `xml:"Affects>Networks>AffectedNetwork"`
	AffectedStopPoints []AffectedStopPoint `xml:"Affects>StopPoints>AffectedStopPoint"`

	// Advice string `xml:"Advice>Details"`
}

type AffectedNetwork struct {
	VehicleMode  string
	AffectedLine []AffectedLine
}

type AffectedLine struct {
	OperatorRef      string `xml:"AffectedOperator>OperatorRef"`
	LineRef          string
	PublishedLineRef string
}

type AffectedStopPoint struct {
	StopPointRef  string
	StopPointName string
}
