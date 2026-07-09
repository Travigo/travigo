package ctdf

type JourneyDetailedRail struct {
	Trains []RailTrain `groups:"detailed"`

	Seating []JourneyDetailedRailSeating `groups:"detailed"`

	SleeperAvailable bool                         `groups:"detailed"`
	Sleepers         []JourneyDetailedRailSeating `groups:"detailed"`

	ReservationRequired     bool `groups:"detailed"`
	ReservationBikeRequired bool `groups:"detailed"`
	ReservationRecommended  bool `groups:"detailed"`

	CateringAvailable   bool   `groups:"detailed"`
	CateringDescription string `groups:"detailed"`

	ReplacementBus bool `groups:"detailed"`
}

type RailTrain struct {
	ID       string `groups:"basic"`
	Position int    `groups:"detailed"`

	AllocationSequence int    `groups:"detailed"`
	ValidFrom          string `groups:"detailed"`
	ValidUntil         string `groups:"detailed"`
	Reversed           bool   `groups:"detailed"`

	VehicleType     string `groups:"detailed"`
	VehicleTypeName string `groups:"detailed"`
	PowerType       string `groups:"detailed"`

	FleetID             string `groups:"detailed"`
	ResourceGroupType   string `groups:"detailed"`
	ResourceGroupStatus string `groups:"detailed"`

	TrainLength int            `groups:"detailed"`
	Carriages   []RailCarriage `groups:"detailed"`

	SpeedKMH int `groups:"detailed"`

	AirConditioning bool `groups:"detailed"`

	WiFi           bool `groups:"detailed"`
	Toilets        bool `groups:"detailed"`
	PowerPlugs     bool `groups:"detailed"`
	USBPlugs       bool `groups:"detailed"`
	DisabledAccess bool `groups:"detailed"`
	BicycleSpaces  bool `groups:"detailed"`
}

type JourneyDetailedRailSeating string

const (
	JourneyDetailedRailSeatingFirst    JourneyDetailedRailSeating = "First"
	JourneyDetailedRailSeatingStandard JourneyDetailedRailSeating = "Standard"
	JourneyDetailedRailSeatingUnknown  JourneyDetailedRailSeating = "Unknown"
)

type RailCarriage struct {
	ID             string                       `groups:"basic"`
	CarriageType   string                       `groups:"basic"`
	SeatingClasses []JourneyDetailedRailSeating `groups:"basic"`
	Toilets        []RailCarriageToilet         `groups:"basic"`

	CarriageID             string `groups:"detailed"`
	VehicleID              string `groups:"detailed"`
	VehiclePosition        int    `groups:"detailed"`
	SpecificType           string `groups:"detailed"`
	Livery                 string `groups:"detailed"`
	SpecialCharacteristics string `groups:"detailed"`
	VehicleStatus          string `groups:"detailed"`
	RegisteredStatus       string `groups:"detailed"`
	LengthMM               int    `groups:"detailed"`
	WeightKG               int    `groups:"detailed"`
	SeatCount              int    `groups:"detailed"`

	Occupancy int `groups:"basic"`
}

type RailCarriageToilet struct {
	Type   string `groups:"basic"`
	Status string `groups:"basic"`
}
