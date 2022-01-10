package transxchange

type VehicleJourney struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	SequenceNumber       string `xml:",attr"`

	PrivateCode        string
	OperatorRef        string
	Direction          string
	GarageRef          string
	VehicleJourneyCode string
	ServiceRef         string
	LineRef            string
	JourneyPatternRef  string
	DepartureTime      string

	VehicleJourneyTimingLinks []VehicleJourneyTimingLink `xml:"VehicleJourneyTimingLink"`

	OperatingProfile OperatingProfile // `xml:",innerxml" json:"-" bson:"-"`
}

func (v *VehicleJourney) GetVehicleJourneyTimingLinkByJourneyPatternTimingLinkRef(ID string) *VehicleJourneyTimingLink {
	for _, vehicleJourneyTimingLink := range v.VehicleJourneyTimingLinks {
		if vehicleJourneyTimingLink.JourneyPatternTimingLinkRef == ID {
			return &vehicleJourneyTimingLink
		}
	}

	return nil
}

type VehicleJourneyTimingLink struct {
	ID string `xml:"id,attr"`

	JourneyPatternTimingLinkRef string
	RunTime                     string
}
