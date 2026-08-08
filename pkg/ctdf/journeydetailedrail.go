package ctdf

type JourneyDetailedRail struct {
	Trains []RailTrain `groups:"detailed,web-journey,web-journey-realtime"`

	Seating []JourneyDetailedRailSeating `groups:"detailed,web-journey,web-journey-realtime"`

	SleeperAvailable bool                         `groups:"detailed,web-journey,web-journey-realtime"`
	Sleepers         []JourneyDetailedRailSeating `groups:"detailed,web-journey,web-journey-realtime"`

	ReservationRequired     bool `groups:"detailed,web-journey,web-journey-realtime"`
	ReservationBikeRequired bool `groups:"detailed,web-journey,web-journey-realtime"`
	ReservationRecommended  bool `groups:"detailed,web-journey,web-journey-realtime"`

	CateringAvailable   bool   `groups:"detailed,web-journey,web-journey-realtime"`
	CateringDescription string `groups:"detailed,web-journey,web-journey-realtime"`

	ReplacementBus bool `groups:"detailed,web-journey,web-journey-realtime"`
}

type RailTrain struct {
	ID       string `groups:"basic,web-journey,web-journey-realtime"`
	Position int    `groups:"detailed,web-journey,web-journey-realtime"`

	AllocationSequence int    `groups:"detailed,web-journey,web-journey-realtime"`
	ValidFrom          string `groups:"detailed"`
	ValidUntil         string `groups:"detailed"`
	Reversed           bool   `groups:"detailed,web-journey,web-journey-realtime"`

	VehicleType     string `groups:"detailed,web-journey,web-journey-realtime"`
	VehicleTypeName string `groups:"detailed,web-journey,web-journey-realtime"`
	PowerType       string `groups:"detailed,web-journey,web-journey-realtime"`

	FleetID string `groups:"detailed,web-journey,web-journey-realtime"`

	TrainLength int            `groups:"detailed,web-journey,web-journey-realtime"`
	Carriages   []RailCarriage `groups:"detailed,web-journey,web-journey-realtime"`

	SpeedKMH int `groups:"detailed,web-journey,web-journey-realtime"`

	AirConditioning bool `groups:"detailed,web-journey,web-journey-realtime"`

	WiFi           bool `groups:"detailed,web-journey,web-journey-realtime"`
	Toilets        bool `groups:"detailed,web-journey,web-journey-realtime"`
	PowerPlugs     bool `groups:"detailed,web-journey,web-journey-realtime"`
	USBPlugs       bool `groups:"detailed,web-journey,web-journey-realtime"`
	DisabledAccess bool `groups:"detailed,web-journey,web-journey-realtime"`
	BicycleSpaces  bool `groups:"detailed,web-journey,web-journey-realtime"`
}

type JourneyDetailedRailSeating string

const (
	JourneyDetailedRailSeatingFirst    JourneyDetailedRailSeating = "First"
	JourneyDetailedRailSeatingStandard JourneyDetailedRailSeating = "Standard"
	JourneyDetailedRailSeatingUnknown  JourneyDetailedRailSeating = "Unknown"
)

type RailCarriageVehicleRole string

const (
	RailCarriageVehicleRolePassenger RailCarriageVehicleRole = "Passenger"
	RailCarriageVehicleRolePowerCar  RailCarriageVehicleRole = "PowerCar"
	RailCarriageVehicleRoleUnknown   RailCarriageVehicleRole = "Unknown"
)

func (carriage RailCarriage) CountsTowardsTrainLength() bool {
	return carriage.VehicleRole != RailCarriageVehicleRolePowerCar
}

type RailCarriage struct {
	ID             string                       `groups:"basic,web-journey,web-journey-realtime"`
	CarriageType   string                       `groups:"basic,web-journey,web-journey-realtime"`
	VehicleRole    RailCarriageVehicleRole      `groups:"basic,web-journey,web-journey-realtime"`
	SeatingClasses []JourneyDetailedRailSeating `groups:"basic,web-journey,web-journey-realtime"`
	Toilets        []RailCarriageToilet         `groups:"basic,web-journey,web-journey-realtime"`

	CarriageID      string `groups:"detailed,web-journey,web-journey-realtime"`
	VehicleID       string `groups:"detailed,web-journey,web-journey-realtime"`
	VehiclePosition int    `groups:"detailed,web-journey,web-journey-realtime"`
	SpecificType    string `groups:"detailed,web-journey,web-journey-realtime"`
	Livery          string `groups:"detailed,web-journey,web-journey-realtime"`
	LengthMM        int    `groups:"detailed,web-journey,web-journey-realtime"`
	SeatCount       int    `groups:"detailed,web-journey,web-journey-realtime"`

	Occupancy int `groups:"basic,web-journey,web-journey-realtime"`
}

type RailCarriageToilet struct {
	Type   string `groups:"basic,web-journey,web-journey-realtime"`
	Status string `groups:"basic,web-journey,web-journey-realtime"`
}
