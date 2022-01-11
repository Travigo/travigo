package ctdf

type Availability struct {
	Match     []AvailabilityRecord // Must match at last one
	Condition []AvailabilityRecord // Must match all
	Exclude   []AvailabilityRecord // Must not match one
}

type AvailabilityRecord struct {
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
