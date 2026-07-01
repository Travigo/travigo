package linxconsist

import "encoding/xml"

type PassengerTrainConsistMessage struct {
	XMLName xml.Name `xml:"PassengerTrainConsistMessage"`

	MessageHeader                  MessageHeader                  `xml:"MessageHeader"`
	MessageStatus                  string                         `xml:"MessageStatus"`
	TrainOperationalIdentification TrainOperationalIdentification `xml:"TrainOperationalIdentification"`
	OperationalTrainNumber         OperationalTrainNumber         `xml:"OperationalTrainNumberIdentifier"`
	ResponsibleRU                  string                         `xml:"ResponsibleRU"`
	Allocations                    []Allocation                   `xml:"Allocation"`
}

type MessageHeader struct {
	MessageReference MessageReference `xml:"MessageReference"`
}

type MessageReference struct {
	MessageType        string `xml:"MessageType"`
	MessageTypeVersion string `xml:"MessageTypeVersion"`
	MessageIdentifier  string `xml:"MessageIdentifier"`
	MessageDateTime    string `xml:"MessageDateTime"`
}

type TrainOperationalIdentification struct {
	TransportOperationalIdentifiers []TransportOperationalIdentifier `xml:"TransportOperationalIdentifiers"`
}

type TransportOperationalIdentifier struct {
	ObjectType    string `xml:"ObjectType"`
	Company       string `xml:"Company"`
	Core          string `xml:"Core"`
	Variant       string `xml:"Variant"`
	TimetableYear string `xml:"TimetableYear"`
	StartDate     string `xml:"StartDate"`
}

type OperationalTrainNumber struct {
	OperationalTrainNumber      string `xml:"OperationalTrainNumber"`
	ScheduledTimeAtHandover     string `xml:"ScheduledTimeAtHandover"`
	ScheduledDateTimeAtTransfer string `xml:"ScheduledDateTimeAtTransfer"`
}

type Allocation struct {
	AllocationSequenceNumber      int           `xml:"AllocationSequenceNumber"`
	TrainOriginDateTime           string        `xml:"TrainOriginDateTime"`
	TrainDestDateTime             string        `xml:"TrainDestDateTime"`
	ResourceGroupPosition         int           `xml:"ResourceGroupPosition"`
	DiagramDate                   string        `xml:"DiagramDate"`
	DiagramNo                     string        `xml:"DiagramNo"`
	AllocationOriginDateTime      string        `xml:"AllocationOriginDateTime"`
	AllocationDestinationDateTime string        `xml:"AllocationDestinationDateTime"`
	Reversed                      string        `xml:"Reversed"`
	ResourceGroup                 ResourceGroup `xml:"ResourceGroup"`
}

type ResourceGroup struct {
	ResourceGroupID     string    `xml:"ResourceGroupId"`
	TypeOfResource      string    `xml:"TypeOfResource"`
	FleetID             string    `xml:"FleetId"`
	ResourceGroupStatus string    `xml:"ResourceGroupStatus"`
	Vehicles            []Vehicle `xml:"Vehicle"`
}

type Vehicle struct {
	VehicleID              string   `xml:"VehicleId"`
	TypeOfVehicle          string   `xml:"TypeOfVehicle"`
	ResourcePosition       int      `xml:"ResourcePosition"`
	PlannedResourceGroup   string   `xml:"PlannedResourceGroup"`
	SpecificType           string   `xml:"SpecificType"`
	Length                 Measure  `xml:"Length"`
	Weight                 int      `xml:"Weight"`
	Livery                 string   `xml:"Livery"`
	Decor                  string   `xml:"Decor"`
	SpecialCharacteristics string   `xml:"SpecialCharacteristics"`
	NumberOfSeats          int      `xml:"NumberOfSeats"`
	VehicleStatus          string   `xml:"VehicleStatus"`
	RegisteredStatus       string   `xml:"RegisteredStatus"`
	MaximumSpeed           int      `xml:"MaximumSpeed"`
	Defects                []Defect `xml:"Defect"`
}

type Measure struct {
	Value   int    `xml:"Value"`
	Measure string `xml:"Measure"`
}

type Defect struct {
	MaintenanceUID            string `xml:"MaintenanceUID"`
	DefectCode                string `xml:"DefectCode"`
	MaintenanceDefectLocation string `xml:"MaintenanceDefectLocation"`
	DefectDescription         string `xml:"DefectDescription"`
	DefectStatus              string `xml:"DefectStatus"`
}
