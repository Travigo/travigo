package ctdf

import (
	"io"
	"strconv"
	"time"
)

const OSMStopIDFormat = "osm-stop-%s"

type OSMElementType string

const (
	OSMElementTypeNode     OSMElementType = "node"
	OSMElementTypeWay      OSMElementType = "way"
	OSMElementTypeRelation OSMElementType = "relation"
)

type OSMStopFeatureType string

const (
	OSMStopFeatureTypeStation        OSMStopFeatureType = "Station"
	OSMStopFeatureTypeStopArea       OSMStopFeatureType = "StopArea"
	OSMStopFeatureTypeStopPosition   OSMStopFeatureType = "StopPosition"
	OSMStopFeatureTypePlatform       OSMStopFeatureType = "Platform"
	OSMStopFeatureTypePlatformEdge   OSMStopFeatureType = "PlatformEdge"
	OSMStopFeatureTypeEntrance       OSMStopFeatureType = "Entrance"
	OSMStopFeatureTypeTrack          OSMStopFeatureType = "Track"
	OSMStopFeatureTypeRoad           OSMStopFeatureType = "Road"
	OSMStopFeatureTypeAccess         OSMStopFeatureType = "Access"
	OSMStopFeatureTypeShop           OSMStopFeatureType = "Shop"
	OSMStopFeatureTypeCafe           OSMStopFeatureType = "Cafe"
	OSMStopFeatureTypeRestaurant     OSMStopFeatureType = "Restaurant"
	OSMStopFeatureTypeFastFood       OSMStopFeatureType = "FastFood"
	OSMStopFeatureTypePub            OSMStopFeatureType = "Pub"
	OSMStopFeatureTypeBar            OSMStopFeatureType = "Bar"
	OSMStopFeatureTypeToilets        OSMStopFeatureType = "Toilets"
	OSMStopFeatureTypeATM            OSMStopFeatureType = "ATM"
	OSMStopFeatureTypeCarPark        OSMStopFeatureType = "CarPark"
	OSMStopFeatureTypeBicyclePark    OSMStopFeatureType = "BicyclePark"
	OSMStopFeatureTypeMotorcyclePark OSMStopFeatureType = "MotorcyclePark"
	OSMStopFeatureTypeAmenity        OSMStopFeatureType = "Amenity"
	OSMStopFeatureTypeOther          OSMStopFeatureType = "Other"
)

type OSMStopParkingAssociation string

const (
	OSMStopParkingOfficial  OSMStopParkingAssociation = "OfficialStationParking"
	OSMStopParkingLikely    OSMStopParkingAssociation = "LikelyStationParking"
	OSMStopParkingNearby    OSMStopParkingAssociation = "NearbyParking"
	OSMStopParkingUnrelated OSMStopParkingAssociation = "UnrelatedParking"
)

type OSMStopFeatureAssociation string

const (
	OSMStopFeatureAssociationInside OSMStopFeatureAssociation = "Inside"
	OSMStopFeatureAssociationNearby OSMStopFeatureAssociation = "Nearby"
)

type OSMStopMatchMethod string

const (
	OSMStopMatchMethodCRS         OSMStopMatchMethod = "CRS"
	OSMStopMatchMethodTIPLOC      OSMStopMatchMethod = "TIPLOC"
	OSMStopMatchMethodNaPTAN      OSMStopMatchMethod = "NaPTAN"
	OSMStopMatchMethodGTFS        OSMStopMatchMethod = "GTFS"
	OSMStopMatchMethodCoordinate  OSMStopMatchMethod = "Coordinate"
	OSMStopMatchMethodManual      OSMStopMatchMethod = "Manual"
	OSMStopMatchMethodRelationID  OSMStopMatchMethod = "RelationID"
	OSMStopMatchMethodElementID   OSMStopMatchMethod = "ElementID"
	OSMStopMatchMethodUnspecified OSMStopMatchMethod = "Unspecified"
)

type OSMStop struct {
	PrimaryIdentifier string   `groups:"basic,search" bson:",omitempty"`
	OtherIdentifiers  []string `groups:"basic,search" bson:",omitempty"`

	CreationDateTime     time.Time `groups:"detailed" bson:",omitempty"`
	ModificationDateTime time.Time `groups:"detailed" bson:",omitempty"`

	DataSource *DataSourceReference `groups:"detailed" bson:",omitempty"`

	StopRef string `groups:"basic,search" bson:",omitempty"`

	TransportTypes []TransportType `groups:"basic,search" bson:",omitempty"`

	Match OSMStopMatch `groups:"detailed" bson:",omitempty"`
	Query OSMStopQuery `groups:"internal" bson:",omitempty"`

	Station  *OSMElementRef `groups:"basic,detailed" bson:",omitempty"`
	StopArea *OSMElementRef `groups:"basic,detailed" bson:",omitempty"`

	Features []OSMStopFeature `groups:"detailed" bson:",omitempty"`
	Elements []OSMElement     `groups:"internal" bson:",omitempty"`
}

type OSMStopMatch struct {
	Method         OSMStopMatchMethod `groups:"detailed" bson:",omitempty"`
	MatchedValue   string             `groups:"detailed" bson:",omitempty"`
	Confidence     float64            `groups:"detailed" bson:",omitempty"`
	DistanceMetres float64            `groups:"detailed" bson:",omitempty"`
}

type OSMStopQuery struct {
	OverpassQuery string    `groups:"internal" bson:",omitempty"`
	Endpoint      string    `groups:"internal" bson:",omitempty"`
	QueriedAt     time.Time `groups:"internal" bson:",omitempty"`
	Location      *Location `groups:"internal" bson:",omitempty"`
	RadiusMetres  int       `groups:"internal" bson:",omitempty"`
}

type OSMStopFeature struct {
	Type OSMStopFeatureType `groups:"detailed" bson:",omitempty"`

	Element OSMElementRef `groups:"detailed" bson:",omitempty"`
	Role    string        `groups:"detailed" bson:",omitempty"`

	PrimaryName string            `groups:"detailed" bson:",omitempty"`
	Ref         string            `groups:"detailed" bson:",omitempty"`
	LocalRef    string            `groups:"detailed" bson:",omitempty"`
	Tags        map[string]string `groups:"internal" bson:",omitempty"`

	ParkingAssociation OSMStopParkingAssociation `groups:"detailed" bson:",omitempty"`
	ParkingConfidence  float64                   `groups:"detailed" bson:",omitempty"`
	Association        OSMStopFeatureAssociation `groups:"detailed" bson:",omitempty"`
	DistanceMetres     float64                   `groups:"detailed" bson:",omitempty"`

	Location *Location  `groups:"detailed" bson:",omitempty"`
	Geometry []Location `groups:"internal" bson:",omitempty"`
}

type OSMElement struct {
	Type OSMElementType `groups:"internal" bson:",omitempty"`
	ID   int64          `groups:"internal" bson:",omitempty"`

	Tags     map[string]string   `groups:"internal" bson:",omitempty"`
	Bounds   *OSMBounds          `groups:"internal" bson:",omitempty"`
	Center   *Location           `groups:"internal" bson:",omitempty"`
	Point    *Location           `groups:"internal" bson:",omitempty"`
	Geometry []Location          `groups:"internal" bson:",omitempty"`
	Members  []OSMRelationMember `groups:"internal" bson:",omitempty"`
}

type OSMElementRef struct {
	Type OSMElementType `groups:"detailed" bson:",omitempty"`
	ID   int64          `groups:"detailed" bson:",omitempty"`
}

type OSMRelationMember struct {
	Type OSMElementType `groups:"internal" bson:",omitempty"`
	ID   int64          `groups:"internal" bson:",omitempty"`
	Role string         `groups:"internal" bson:",omitempty"`

	Geometry []Location `groups:"internal" bson:",omitempty"`
}

type OSMBounds struct {
	MinLat float64 `groups:"internal" bson:",omitempty"`
	MinLon float64 `groups:"internal" bson:",omitempty"`
	MaxLat float64 `groups:"internal" bson:",omitempty"`
	MaxLon float64 `groups:"internal" bson:",omitempty"`
}

func (osmStop *OSMStop) GetPrimaryIdentifier() string {
	return osmStop.PrimaryIdentifier
}

func (osmStop *OSMStop) GetCreationDateTime() time.Time {
	return osmStop.CreationDateTime
}

func (osmStop *OSMStop) SetPrimaryIdentifier(id string) {
	osmStop.PrimaryIdentifier = id
}

func (osmStop *OSMStop) SetOtherIdentifiers(ids []string) {
	osmStop.OtherIdentifiers = ids
}

func (osmStop *OSMStop) GenerateDeterministicID(writer io.Writer) {
	writer.Write([]byte(osmStop.StopRef))

	if osmStop.StopArea != nil {
		writer.Write([]byte(osmStop.StopArea.Type))
		writer.Write([]byte(strconv.FormatInt(osmStop.StopArea.ID, 10)))
	}
}
