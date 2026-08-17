package ctdf

import "time"

// UserCommute defines the two time constraints for a repeat journey. The
// outbound trip arrives at DestinationRef by ArrivalAtDestinationTime; the
// return trip leaves DestinationRef at or after ReturnDepartureTime.
type UserCommute struct {
	PrimaryIdentifier string `bson:"primaryidentifier" json:"id" groups:"web-commute"`
	UserID            string `bson:"userid" json:"-"`

	Name                     string   `bson:"name" json:"name" groups:"web-commute"`
	OriginRef                string   `bson:"originref" json:"originRef" groups:"web-commute"`
	DestinationRef           string   `bson:"destinationref" json:"destinationRef" groups:"web-commute"`
	DaysOfWeek               []string `bson:"daysofweek" json:"daysOfWeek" groups:"web-commute"`
	ArrivalAtDestinationTime string   `bson:"arrivalatdestinationtime" json:"arrivalAtDestinationTime" groups:"web-commute"`
	ReturnDepartureTime      string   `bson:"returndeparturetime" json:"returnDepartureTime" groups:"web-commute"`

	CreationDateTime     time.Time `bson:"creationdatetime" json:"createdAt" groups:"web-commute"`
	ModificationDateTime time.Time `bson:"modificationdatetime" json:"updatedAt" groups:"web-commute"`

	Origin      *CommuteStop `bson:"-" json:"origin,omitempty" groups:"web-commute"`
	Destination *CommuteStop `bson:"-" json:"destination,omitempty" groups:"web-commute"`
}

type CommuteStop struct {
	PrimaryIdentifier string `json:"id"`
	PrimaryName       string `json:"name"`
	Descriptor        string `json:"descriptor,omitempty"`
}

func NewCommuteStop(stop *Stop) *CommuteStop {
	if stop == nil {
		return nil
	}
	return &CommuteStop{PrimaryIdentifier: stop.PrimaryIdentifier, PrimaryName: stop.PrimaryName, Descriptor: stop.Descriptor}
}
