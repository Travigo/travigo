package ctdf

type JourneyDetailedRail struct {
	VehicleType string `groups:"detailed"`

	Seating JourneyDetailedRailSeating `groups:"detailed"`

	SleeperAvailable bool                       `groups:"detailed"`
	Sleepers         JourneyDetailedRailSeating `groups:"detailed"`

	SpeedKMH int `groups:"detailed"`

	AirConditioning bool `groups:"detailed"`
	Heating         bool `groups:"detailed"`

	DriverOnly    bool `groups:"detailed"`
	GuardRequired bool `groups:"detailed"`

	ReservationRequired     bool `groups:"detailed"`
	ReservationBikeRequired bool `groups:"detailed"`
	ReservationRecommended  bool `groups:"detailed"`

	CateringAvailable   bool   `groups:"detailed"`
	CateringDescription string `groups:"detailed"`
}

type JourneyDetailedRailSeating string

const (
	JourneyDetailedRailSeatingFirstStandard JourneyDetailedRailSeating = "FirstStandard"
	JourneyDetailedRailSeatingFirst                                    = "First"
	JourneyDetailedRailSeatingStandard                                 = "Standard"
	JourneyDetailedRailSeatingUnknown                                  = "Unknown"
)
