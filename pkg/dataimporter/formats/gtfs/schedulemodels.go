package gtfs

type Agency struct {
	ID       string `csv:"agency_id"`
	Name     string `csv:"agency_name"`
	URL      string `csv:"agency_url"`
	Timezone string `csv:"agency_timezone"`
	Language string `csv:"agency_lang"`
	Phone    string `csv:"agency_phone"`
	FareURL  string `csv:"agency_fare_url"`
	Email    string `csv:"agency_email"`

	NOC string `csv:"agency_noc"` // UK ONLY
}

type Stop struct {
	ID           string  `csv:"stop_id"`
	Code         string  `csv:"stop_code"`
	Name         string  `csv:"stop_name"`
	Description  string  `csv:"stop_desc"`
	Latitude     float64 `csv:"stop_lat"`
	Longitude    float64 `csv:"stop_lon"`
	ZoneID       string  `csv:"zone_id"`
	URL          string  `csv:"stop_url"`
	Type         string  `csv:"location_type"`
	Parent       string  `csv:"parent_station"`
	Timezone     string  `csv:"stop_timezone"`
	Wheelchair   string  `csv:"wheelchair_boarding"`
	LevelID      string  `csv:"level_id"`
	PlatformCode string  `csv:"platform_code"`
}

type Route struct {
	ID                string `csv:"route_id"`
	AgencyID          string `csv:"agency_id"`
	ShortName         string `csv:"route_short_name"`
	LongName          string `csv:"route_long_name"`
	Description       string `csv:"route_desc"`
	URL               string `csv:"route_url"`
	Colour            string `csv:"route_color"`
	TextColour        string `csv:"route_text_color"`
	NetworkID         string `csv:"network_id"`
	Type              int    `csv:"route_type"`
	SortOrder         int    `csv:"route_sort_order"`
	ContinuousPickup  int    `csv:"continuous_pickup"`
	ContinuousDropOff int    `csv:"continuous_drop_off"`
}

type Trip struct {
	RouteID              string `csv:"route_id"`
	ServiceID            string `csv:"service_id"`
	ID                   string `csv:"trip_id"`
	Headsign             string `csv:"trip_headsign"`
	Name                 string `csv:"trip_short_name"`
	BlockID              string `csv:"block_id"`
	ShapeID              string `csv:"shape_id"`
	WheelchairAccessible int8   `csv:"wheelchair_accessible"`
	BikesAllowed         int8   `csv:"bikes_allowed"`
	DirectionID          bool   `csv:"direction_id"`
}

type StopTime struct {
	TripID        string `csv:"trip_id"`
	ArrivalTime   string `csv:"arrival_time"`
	DepartureTime string `csv:"departure_time"`
	StopID        string `csv:"stop_id"`
	StopHeadsign  string `csv:"stop_headsign"`
	StopSequence  int    `csv:"stop_sequence"`
	PickupType    int8   `csv:"pickup_type"`
	DropOffType   int8   `csv:"drop_off_type"`
	// ContinuousPickup       string  `csv:"continuous_pickup"`
	// ContinuousDropOff      string  `csv:"continuous_drop_off"`
	// ShapeDistanceTravelled float64 `csv:"shape_dist_traveled"`
	// Timepoint              string  `csv:"timepoint"`
}

type Calendar struct {
	ServiceID string `csv:"service_id"`
	Monday    int    `csv:"monday"`
	Tuesday   int    `csv:"tuesday"`
	Wednesday int    `csv:"wednesday"`
	Thursday  int    `csv:"thursday"`
	Friday    int    `csv:"friday"`
	Saturday  int    `csv:"saturday"`
	Sunday    int    `csv:"sunday"`
	Start     string `csv:"start_date"`
	End       string `csv:"end_date"`
}

func (c *Calendar) GetRunningDays() []string {
	days := []string{}

	if c.Monday == 1 {
		days = append(days, "Monday")
	}
	if c.Tuesday == 1 {
		days = append(days, "Tuesday")
	}
	if c.Wednesday == 1 {
		days = append(days, "Wednesday")
	}
	if c.Thursday == 1 {
		days = append(days, "Thursday")
	}
	if c.Friday == 1 {
		days = append(days, "Friday")
	}
	if c.Saturday == 1 {
		days = append(days, "Saturday")
	}
	if c.Sunday == 1 {
		days = append(days, "Sunday")
	}

	return days
}

type CalendarDate struct {
	ServiceID     string `csv:"service_id"`
	Date          string `csv:"date"`
	ExceptionType int    `csv:"exception_type"`
}

type Frequency struct {
	TripID         string `csv:"trip_id"`
	StartTime      string `csv:"start_time"`
	EndTime        string `csv:"end_time"`
	HeadwaySeconds int    `csv:"headway_secs"`
	ExactTimes     string `csv:"exact_times"`
}

type Shape struct {
	ID               string  `csv:"shape_id"`
	PointLatitude    float64 `csv:"shape_pt_lat"`
	PointLongitude   float64 `csv:"shape_pt_lon"`
	PointSequence    int     `csv:"shape_pt_sequence"`
	DistanceTraveled float64 `csv:"shape_dist_traveled"`
}

type Translation struct {
	TableName   string `csv:"table_name"`
	FieldName   string `csv:"field_name"`
	Language    string `csv:"language"`
	Translation string `csv:"translation"`
	RecordID    string `csv:"record_id"`
	RecordSubID string `csv:"record_sub_id"`
	FieldValue  string `csv:"field_value"`
}
