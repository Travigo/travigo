package ctdf

import (
	"strings"
	"time"
)

type TimetableRecord struct {
	Journey *Journey `groups:"basic"`
	// Service *Service `groups:"basic"`
	// Operator *Operator `groups:"basic"`

	Time time.Time `groups:"basic"`
}

const YearMonthDayFormat = "2006-01-02"

var daysOfWeek = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func datesMatch(a time.Time, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func checkRule(rule *AvailabilityRule, dateTime time.Time) bool {
	dayOfWeek := daysOfWeek[dateTime.Weekday()]

	switch rule.Type {
	case AvailabilityDayOfWeek:
		return rule.Value == dayOfWeek
	case AvailabilityDate:
		ruleDateTime, _ := time.Parse(YearMonthDayFormat, rule.Value)
		return datesMatch(ruleDateTime, dateTime)
	case AvailabilityDateRange:
		splitDateRange := strings.Split(rule.Value, ":")

		var startDate time.Time
		if splitDateRange[0] == "" {
			startDate, _ = time.Parse(YearMonthDayFormat, "0-0-0")
		} else {
			startDate, _ = time.Parse(YearMonthDayFormat, splitDateRange[0])
		}

		var endDate time.Time
		if splitDateRange[1] == "" {
			endDate, _ = time.Parse(YearMonthDayFormat, "3022-12-24")
		} else {
			endDate, _ = time.Parse(YearMonthDayFormat, splitDateRange[1])
		}

		return (dateTime.After(startDate) && dateTime.Before(endDate)) || datesMatch(startDate, dateTime) || datesMatch(endDate, dateTime)
	// case AvailabilitySpecialDay:
	case AvailabilityMatchAll:
		return true
	default:
		// log.Error().Msgf("Cannot parse rule type %s", rule.Type)
		return false
	}
}

func GenerateTimetableFromJourneys(journeys []*Journey, stopRef string, dateTime time.Time) []*TimetableRecord {
	timetable := []*TimetableRecord{}

	// pretty.Println(checkRule(&AvailabilityRule{
	// 	Type:  AvailabilityDateRange,
	// 	Value: "2022-01-11:",
	// }, dateTime))

	// TODO: add dynamic destination display

	for _, journey := range journeys {
		var stopDeperatureTime time.Time

		for _, path := range journey.Path {
			if path.OriginStopRef == stopRef {
				refTime := path.OriginDepartureTime
				stopDeperatureTime = path.OriginDepartureTime
				stopDeperatureTime = time.Date(
					dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), refTime.Location(),
				)
				break
			}
		}

		if stopDeperatureTime.Before(dateTime) {
			continue
		}

		availability := journey.Availability

		matchHit := false
		matchSecondaryHit := false
		conditionHit := true
		excludeHit := false

		// Parse all the Match - if any are true then mark the matchHit as true
		for _, rule := range availability.Match {
			if checkRule(&rule, dateTime) {
				matchHit = true
			}
		}
		// Parse all the MatchSecondary - if any are true then mark the matchSecondaryHit as true
		for _, rule := range availability.MatchSecondary {
			if checkRule(&rule, dateTime) {
				matchSecondaryHit = true
			}
		}
		// Parse all the Condition - if any are false then mark the conditionHit as false
		for _, rule := range availability.Condition {
			if !checkRule(&rule, dateTime) {
				conditionHit = false
			}
		}
		// Parse all the Exclude - if any are true then mark the excludeHit as true
		for _, rule := range availability.Exclude {
			if checkRule(&rule, dateTime) {
				excludeHit = true
			}
		}

		// If theres nothing in MatchSecondary then just set it as true
		if len(availability.MatchSecondary) == 0 {
			matchSecondaryHit = true
		}

		if matchHit && matchSecondaryHit && conditionHit && !excludeHit {
			journey.GetReferences()

			timetable = append(timetable, &TimetableRecord{
				Journey: journey,
				Time:    stopDeperatureTime,
			})
		}
	}

	return timetable
}
