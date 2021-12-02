package transxchange

type JourneyPatternSection struct {
	ID string `xml:"id,attr"`

	JourneyPatternTimingLinks []JourneyPatternTimingLink `xml:"JourneyPatternTimingLink"`
}

type JourneyPatternTimingLink struct {
	ID string `xml:"id,attr"`

	RouteLinkRef string
	RunTime      string

	From JourneyPatternTimingLinkPoint
	To   JourneyPatternTimingLinkPoint
}

type JourneyPatternTimingLinkPoint struct {
	ID             string `xml:"id,attr"`
	SequenceNumber string `xml:",attr"`

	Activity                  string
	DynamicDestinationDisplay string
	StopPointRef              string
	TimingStatus              string
	FareStageNumber           string
}
