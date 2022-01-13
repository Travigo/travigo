package ctdf

type Availability struct {
	Match          []AvailabilityRule // Must match at least one
	MatchSecondary []AvailabilityRule // Must match at least one if exists
	Condition      []AvailabilityRule // Must match all
	Exclude        []AvailabilityRule // Must not match one
}

type AvailabilityRule struct {
	Type        AvailabilityRecordType
	Value       string
	Description string
}

type AvailabilityRecordType string

const (
	AvailabilityDayOfWeek  AvailabilityRecordType = "DayOfWeek"
	AvailabilityDate                              = "Date"
	AvailabilityDateRange                         = "DateRange"
	AvailabilitySpecialDay                        = "SpecialDay"
	AvailabilityMatchAll                          = "MatchAll"
)
