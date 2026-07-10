package ctdf

import "time"

const StopTransferIDFormat = "stop-transfer-%s-%s"

type StopTransferType string

const (
	StopTransferTypeNearbyWalk    StopTransferType = "NearbyWalk"
	StopTransferTypeSameStopGroup StopTransferType = "SameStopGroup"
	StopTransferTypePlatformAlias StopTransferType = "PlatformAlias"
	StopTransferTypeRecommended   StopTransferType = "Recommended"
	StopTransferTypeTimed         StopTransferType = "Timed"
	StopTransferTypeMinimumTime   StopTransferType = "MinimumTime"
	StopTransferTypeForbidden     StopTransferType = "Forbidden"
	StopTransferTypeInSeat        StopTransferType = "InSeat"
)

type StopTransfer struct {
	PrimaryIdentifier string `groups:"basic,detailed" bson:",omitempty"`

	FromStopRef  string `groups:"basic,detailed" bson:",omitempty"`
	ToStopRef    string `groups:"basic,detailed" bson:",omitempty"`
	FromRouteRef string `groups:"detailed" bson:",omitempty"`
	ToRouteRef   string `groups:"detailed" bson:",omitempty"`
	FromTripRef  string `groups:"detailed" bson:",omitempty"`
	ToTripRef    string `groups:"detailed" bson:",omitempty"`

	Type StopTransferType `groups:"basic,detailed" bson:",omitempty"`

	DistanceMetres           int `groups:"basic,detailed" bson:",omitempty"`
	WalkDurationSeconds      int `groups:"basic,detailed" bson:",omitempty"`
	MinChangeDurationSeconds int `groups:"basic,detailed" bson:",omitempty"`
	TotalDurationSeconds     int `groups:"basic,detailed" bson:",omitempty"`
	GeneratedRadiusMetres    int `groups:"detailed" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`
}
