package transxchange

type Service struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	ServiceCode              string
	TicketMachineServiceCode string
	RegisteredOperatorRef    string
	PublicUse                bool
	StartDate                string `xml:"OperatingPeriod>StartDate"`
	EndDate                  string `xml:"OperatingPeriod>EndDate"`

	Lines []Line `xml:"Lines>Line"`

	Origin           string `xml:"StandardService>Origin"`
	Destination      string `xml:"StandardService>Destination"`
	UseAllStopPoints string `xml:"StandardService>UseAllStopPoints"`

	JourneyPatterns []JourneyPattern `xml:"StandardService>JourneyPattern"`
}

type Line struct {
	ID       string `xml:"id,attr"`
	LineName string

	OutboundOrigin      string `xml:"OutboundDescription>Origin"`
	OutboundDestination string `xml:"OutboundDescription>Destination"`
	OutboundDescription string `xml:"OutboundDescription>Description"`

	InboundOrigin      string `xml:"InboundDescription>Origin"`
	InboundDestination string `xml:"InboundDescription>Destination"`
	InboundDescription string `xml:"InboundDescription>Description"`
}

type JourneyPattern struct {
	ID                   string `xml:"id,attr"`
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	DestinationDisplay        string
	OperatorRef               string
	Direction                 string
	RouteRef                  string
	JourneyPatternSectionRefs string
}
