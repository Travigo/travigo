package ctdf

type StopDetailed struct {
	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`

	FoodDrink []StopShop    `groups:"detailed" bson:",omitempty"`
	Shops     []StopShop    `groups:"detailed" bson:",omitempty"`
	Toilets   []StopToilets `groups:"detailed" bson:",omitempty"`

	CarPark     []StopParking `groups:"detailed" bson:",omitempty"`
	BicyclePark []StopParking `groups:"detailed" bson:",omitempty"`

	// ATM
	// TICKETS
}

type StopShop struct {
	PrimaryName string `groups:"basic" bson:",omitempty"`
	Type        string `groups:"basic" bson:",omitempty"`
	Website     string `groups:"basic" bson:",omitempty"`

	WikiDataID string `groups:"basic" bson:",omitempty"` // TODO allow us to get logo image?

	LocationDescription string    `groups:"basic" bson:",omitempty"`
	Association         string    `groups:"basic" bson:",omitempty"`
	DistanceMetres      float64   `groups:"basic" bson:",omitempty"`
	Location            *Location `groups:"detailed" bson:",omitempty"`
}

type StopToilets struct {
	CustomerOnly bool `groups:"basic" bson:",omitempty"`
	Cost         bool `groups:"basic" bson:",omitempty"`
	Accessible   bool `groups:"basic" bson:",omitempty"`

	Male   bool `groups:"basic" bson:",omitempty"`
	Female bool `groups:"basic" bson:",omitempty"`

	OpenHoursDescription string `groups:"basic" bson:",omitempty"`

	LocationDescription string    `groups:"basic" bson:",omitempty"`
	Association         string    `groups:"basic" bson:",omitempty"`
	DistanceMetres      float64   `groups:"basic" bson:",omitempty"`
	Location            *Location `groups:"detailed" bson:",omitempty"`
}

type StopParking struct {
	PrimaryName string `groups:"basic" bson:",omitempty"`
	Type        string `groups:"basic" bson:",omitempty"`

	Cost       bool `groups:"basic" bson:",omitempty"`
	Accessible bool `groups:"basic" bson:",omitempty"`

	OperatorName       string  `groups:"basic" bson:",omitempty"`
	OperatorWikiDataID string  `groups:"basic" bson:",omitempty"`
	Association        string  `groups:"basic" bson:",omitempty"`
	DistanceMetres     float64 `groups:"basic" bson:",omitempty"`

	Capacity int  `groups:"basic" bson:",omitempty"`
	Covered  bool `groups:"basic" bson:",omitempty"`
}
