package ctdf

type Availability struct {
	Match   []AvailabilityRecord
	Exclude []AvailabilityRecord
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
