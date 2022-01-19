package ctdf

type Availability struct {
	Match          []AvailabilityRule `groups:"basic"` // Must match at least one
	MatchSecondary []AvailabilityRule `groups:"basic"` // Must match at least one if exists
	Condition      []AvailabilityRule `groups:"basic"` // Must match all
	Exclude        []AvailabilityRule `groups:"basic"` // Must not match one
}

type AvailabilityRule struct {
	Type        AvailabilityRecordType `groups:"basic"`
	Value       string                 `groups:"basic"`
	Description string                 `groups:"basic"`
}

type AvailabilityRecordType string

const (
	AvailabilityDayOfWeek  AvailabilityRecordType = "DayOfWeek"
	AvailabilityDate                              = "Date"
	AvailabilityDateRange                         = "DateRange"
	AvailabilitySpecialDay                        = "SpecialDay"
	AvailabilityMatchAll                          = "MatchAll"
)
