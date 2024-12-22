package ctdf

import (
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type Availability struct {
	Match          []AvailabilityRule `groups:"basic,departureboard-cache"` // Must match at least one
	MatchSecondary []AvailabilityRule `groups:"basic,departureboard-cache"` // Must match at least one if exists
	Condition      []AvailabilityRule `groups:"basic,departureboard-cache"` // Must match all
	Exclude        []AvailabilityRule `groups:"basic,departureboard-cache"` // Must not match one
}

func (availability *Availability) MatchDate(dateTime time.Time) bool {
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

	return matchHit && matchSecondaryHit && conditionHit && !excludeHit
}

type AvailabilityRule struct {
	Type        AvailabilityRecordType `groups:"basic,departureboard-cache"`
	Value       string                 `groups:"basic,departureboard-cache"`
	Description string                 `groups:"basic,departureboard-cache"`
}

type AvailabilityRecordType string

const (
	AvailabilityDayOfWeek AvailabilityRecordType = "DayOfWeek"
	AvailabilityDate                             = "Date"
	AvailabilityDateRange                        = "DateRange"
	AvailabilityMatchAll                         = "MatchAll"
)

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
	case AvailabilityMatchAll:
		return true
	default:
		log.Error().Msgf("Cannot parse rule type %s", rule.Type)
		return false
	}
}
