package ctdf

import "time"

const StopTransferIDFormat = "stop-transfer-%s-%s"

type StopTransferType string

const (
	StopTransferTypeNearbyWalk    StopTransferType = "NearbyWalk"
	StopTransferTypeSameStopGroup StopTransferType = "SameStopGroup"
	StopTransferTypePlatformAlias StopTransferType = "PlatformAlias"
)

type StopTransfer struct {
	PrimaryIdentifier string `groups:"basic,detailed" bson:",omitempty"`

	FromStopRef string `groups:"basic,detailed" bson:",omitempty"`
	ToStopRef   string `groups:"basic,detailed" bson:",omitempty"`

	Type StopTransferType `groups:"basic,detailed" bson:",omitempty"`

	DistanceMetres                    int     `groups:"basic,detailed" bson:",omitempty"`
	WalkDurationSeconds               int     `groups:"basic,detailed" bson:",omitempty"`
	MinChangeDurationSeconds          int     `groups:"basic,detailed" bson:",omitempty"`
	TotalDurationSeconds              int     `groups:"basic,detailed" bson:",omitempty"`
	GeneratedRadiusMetres             int     `groups:"detailed" bson:",omitempty"`
	GeneratedWalkSpeedMetresPerSecond float64 `groups:"detailed" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`
}
