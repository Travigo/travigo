package ctdf

type StopDetailed struct {
	DataSource *DataSourceReference `groups:"detailed,web-stop-detailed" bson:",omitempty"`

	FoodDrink []StopShop    `groups:"detailed,web-stop-detailed" bson:",omitempty"`
	Shops     []StopShop    `groups:"detailed,web-stop-detailed" bson:",omitempty"`
	Toilets   []StopToilets `groups:"detailed,web-stop-detailed" bson:",omitempty"`

	CarPark     []StopParking `groups:"detailed,web-stop-detailed" bson:",omitempty"`
	BicyclePark []StopParking `groups:"detailed,web-stop-detailed" bson:",omitempty"`

	// ATM
	// TICKETS
}

type StopShop struct {
	PrimaryName string `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Type        string `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Website     string `groups:"basic,web-stop-detailed" bson:",omitempty"`

	WikiDataID string `groups:"basic,web-stop-detailed" bson:",omitempty"` // TODO allow us to get logo image?

	LocationDescription string    `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Association         string    `groups:"basic,web-stop-detailed" bson:",omitempty"`
	DistanceMetres      float64   `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Location            *Location `groups:"detailed,web-stop-detailed" bson:",omitempty"`
}

type StopToilets struct {
	CustomerOnly bool `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Cost         bool `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Accessible   bool `groups:"basic,web-stop-detailed" bson:",omitempty"`

	Male   bool `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Female bool `groups:"basic,web-stop-detailed" bson:",omitempty"`

	OpenHoursDescription string `groups:"basic,web-stop-detailed" bson:",omitempty"`

	LocationDescription string    `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Association         string    `groups:"basic,web-stop-detailed" bson:",omitempty"`
	DistanceMetres      float64   `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Location            *Location `groups:"detailed,web-stop-detailed" bson:",omitempty"`
}

type StopParking struct {
	PrimaryName string `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Type        string `groups:"basic,web-stop-detailed" bson:",omitempty"`

	Cost       bool `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Accessible bool `groups:"basic,web-stop-detailed" bson:",omitempty"`

	OperatorName       string  `groups:"basic,web-stop-detailed" bson:",omitempty"`
	OperatorWikiDataID string  `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Association        string  `groups:"basic,web-stop-detailed" bson:",omitempty"`
	DistanceMetres     float64 `groups:"basic,web-stop-detailed" bson:",omitempty"`

	Capacity int  `groups:"basic,web-stop-detailed" bson:",omitempty"`
	Covered  bool `groups:"basic,web-stop-detailed" bson:",omitempty"`
}
