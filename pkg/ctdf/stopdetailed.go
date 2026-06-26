package ctdf

type StopDetailed struct {
	FoodDrink []StopShop    `groups:"detailed" bson:",omitempty"`
	Shops     []StopShop    `groups:"detailed" bson:",omitempty"`
	Toilets   []StopToilets `groups:"detailed" bson:",omitempty"`

	// ATM
	// TICKETS
}

type StopShop struct {
	PrimaryName string `groups:"basic" bson:",omitempty"`
	Type        string `groups:"basic" bson:",omitempty"`
	Website     string `groups:"basic" bson:",omitempty"`

	WikiDataID string `groups:"basic" bson:",omitempty"` // TODO allow us to get logo image?

	LocationDescription string    `groups:"basic" bson:",omitempty"`
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
	Location            *Location `groups:"detailed" bson:",omitempty"`
}
