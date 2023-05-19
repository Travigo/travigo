package transxchange

type Operator struct {
	ID string `xml:"id,attr"`

	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	NationalOperatorCode  string
	OperatorCode          string
	OperatorShortName     string
	OperatorNameOnLicence string
	TradingName           string
	LicenceNumber         string

	Garages []Garage `xml:"Garages>Garage"`
}

type Garage struct {
	GarageCode string
	GarageName string
	Location   Location
}
