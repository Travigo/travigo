package ctdf

type JourneyDetailedRail struct {
	VehicleType     string `groups:"detailed"`
	VehicleTypeName string `groups:"detailed"`
	PowerType       string `groups:"detailed"`

	Seating JourneyDetailedRailSeating `groups:"detailed"`

	SleeperAvailable bool                       `groups:"detailed"`
	Sleepers         JourneyDetailedRailSeating `groups:"detailed"`

	SpeedKMH int `groups:"detailed"`

	AirConditioning bool `groups:"detailed"`

	WiFi           bool `groups:"detailed"`
	Toilets        bool `groups:"detailed"`
	PowerPlugs     bool `groups:"detailed"`
	USBPlugs       bool `groups:"detailed"`
	DisabledAccess bool `groups:"detailed"`
	BicycleSpaces  bool `groups:"detailed"`

	DriverOnly    bool `groups:"detailed"`
	GuardRequired bool `groups:"detailed"`

	ReservationRequired     bool `groups:"detailed"`
	ReservationBikeRequired bool `groups:"detailed"`
	ReservationRecommended  bool `groups:"detailed"`

	CateringAvailable   bool   `groups:"detailed"`
	CateringDescription string `groups:"detailed"`

	ReplacementBus bool `groups:"detailed"`
}

type JourneyDetailedRailSeating string

const (
	JourneyDetailedRailSeatingFirstStandard JourneyDetailedRailSeating = "FirstStandard"
	JourneyDetailedRailSeatingFirst                                    = "First"
	JourneyDetailedRailSeatingStandard                                 = "Standard"
	JourneyDetailedRailSeatingUnknown                                  = "Unknown"
)
