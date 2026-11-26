package rsps5045

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type railRestrictionCatalog struct {
	restrictions map[string][]ctdf.FareRailRestriction
	calendars    map[string][]ctdf.FareRailCalendar
}

type restrictionKey struct {
	code string
	cf   string
}

// importRailRestrictions retains the machine-readable subset of RSPS5045
// restrictions that the journey planner can evaluate, and marks the other
// restriction record types explicitly so they cannot produce an exact quote.
func (format *Fares) importRailRestrictions() (railRestrictionCatalog, error) {
	catalog := railRestrictionCatalog{
		restrictions: map[string][]ctdf.FareRailRestriction{},
		calendars:    map[string][]ctdf.FareRailCalendar{},
	}
	files := format.filesByExtension(".RST")
	if len(files) == 0 {
		return catalog, nil
	}
	if len(files) != 1 {
		return catalog, fmt.Errorf("RSPS5045 feed has %d RST files", len(files))
	}
	builders := map[restrictionKey]*ctdf.FareRailRestriction{}
	periods := map[string]struct{ from, to time.Time }{}
	timeRules := map[restrictionKey]map[string]int{}
	trainRules := map[restrictionKey]map[string]int{}
	pendingTimeDates := map[restrictionKey]map[string][]ctdf.FareRailDateBand{}
	pendingTimeTOCs := map[restrictionKey]map[string][]string{}
	pendingTrainDates := map[restrictionKey]map[string][]ctdf.FareRailDateBand{}
	getRestriction := func(cf, code string) *ctdf.FareRailRestriction {
		key := restrictionKey{code: code, cf: cf}
		value := builders[key]
		if value == nil {
			value = &ctdf.FareRailRestriction{Code: code}
			builders[key] = value
		}
		return value
	}
	_, _, err := scanFile(files[0], files[0].Name, "RST", nil, func(line []byte, lineNumber int64) error {
		if len(line) < 3 {
			return errors.New("restriction record is shorter than its update marker and type")
		}
		typeCode := strings.TrimSpace(string(line[1:3]))
		cf := strings.TrimSpace(string(line[3:4]))
		code := strings.TrimSpace(string(line[4:6]))
		requireLength := func(want int) error {
			if len(line) != want {
				return fmt.Errorf("RST %s record has length %d; expected %d", typeCode, len(line), want)
			}
			return nil
		}
		switch typeCode {
		case "RD":
			if err := requireLength(20); err != nil {
				return err
			}
			from, err := dateField(line[4:12])
			if err != nil {
				return err
			}
			to, err := dateField(line[12:20])
			if err != nil {
				return err
			}
			if !from.IsZero() && !to.IsZero() && to.Before(from) {
				return fmt.Errorf("restriction date period %s ends before it starts", cf)
			}
			periods[cf] = struct{ from, to time.Time }{from: from, to: to}
		case "RH":
			if err := requireLength(139); err != nil {
				return err
			}
			if cf != "C" && cf != "F" {
				return fmt.Errorf("restriction %s has unsupported current/future marker %q", code, cf)
			}
			outwardMode := strings.TrimSpace(string(line[136:137]))
			returnMode := strings.TrimSpace(string(line[137:138]))
			changeMarker := strings.TrimSpace(string(line[138:139]))
			if (outwardMode != "P" && outwardMode != "N") || (returnMode != "P" && returnMode != "N") || (changeMarker != "Y" && changeMarker != "N") {
				return fmt.Errorf("restriction %s has invalid outward/return/change markers %q/%q/%q", code, outwardMode, returnMode, changeMarker)
			}
			value := getRestriction(cf, code)
			value.Description = strings.TrimSpace(string(line[6:36]))
			value.OutwardDescription = strings.TrimSpace(string(line[36:86]))
			value.ReturnDescription = strings.TrimSpace(string(line[86:136]))
			value.OutwardMode = outwardMode
			value.ReturnMode = returnMode
			value.ChangeAllowed = changeMarker == "Y"
		case "HD":
			if err := requireLength(21); err != nil {
				return err
			}
			value := getRestriction(cf, code)
			value.DateBands = append(value.DateBands, ctdf.FareRailDateBand{FromMonthDay: strings.TrimSpace(string(line[6:10])), ToMonthDay: strings.TrimSpace(string(line[10:14])), Days: strings.TrimSpace(string(line[14:21]))})
		case "HL":
			if err := requireLength(9); err != nil {
				return err
			}
			value := getRestriction(cf, code)
			value.RouteLocations = append(value.RouteLocations, strings.TrimSpace(string(line[6:9])))
		case "HC":
			if err := requireLength(9); err != nil {
				return err
			}
			value := getRestriction(cf, code)
			value.AllowedChangeLocations = append(value.AllowedChangeLocations, strings.TrimSpace(string(line[6:9])))
		case "HA":
			value := getRestriction(cf, code)
			value.Unsupported = append(value.Unsupported, "additional restriction record")
		case "TR":
			if err := requireLength(26); err != nil {
				return err
			}
			sequence := strings.TrimSpace(string(line[6:10]))
			outRet := strings.TrimSpace(string(line[10:11]))
			if sequence == "" || (outRet != "O" && outRet != "R") {
				return fmt.Errorf("restriction %s has an invalid time restriction identity", code)
			}
			timeFrom, err := parseRailClock(string(line[11:15]))
			if err != nil {
				return fmt.Errorf("restriction %s time %s: %w", code, sequence, err)
			}
			timeTo, err := parseRailClock(string(line[15:19]))
			if err != nil {
				return fmt.Errorf("restriction %s time %s: %w", code, sequence, err)
			}
			event := strings.TrimSpace(string(line[19:20]))
			if event != "A" && event != "D" && event != "V" {
				return fmt.Errorf("restriction %s time %s has unsupported event %q", code, sequence, event)
			}
			timetableType := strings.TrimSpace(string(line[23:24]))
			if timetableType != "T" && timetableType != "A" {
				return fmt.Errorf("restriction %s time %s has unsupported timetable type %q", code, sequence, timetableType)
			}
			minimumFareMarker := strings.TrimSpace(string(line[25:26]))
			if minimumFareMarker != "Y" && minimumFareMarker != "N" {
				return fmt.Errorf("restriction %s time %s has invalid minimum-fare marker %q", code, sequence, minimumFareMarker)
			}
			value := getRestriction(cf, code)
			key := restrictionKey{code: code, cf: cf}
			if timeRules[key] == nil {
				timeRules[key] = map[string]int{}
			}
			ruleKey := sequence + ":" + outRet
			timeRules[key][ruleKey] = len(value.TimeRestrictions)
			value.TimeRestrictions = append(value.TimeRestrictions, ctdf.FareRailTimeRestriction{
				Sequence: sequence, OutRet: outRet, TimeFrom: timeFrom, TimeTo: timeTo,
				Event: event, LocationCRS: strings.TrimSpace(string(line[20:23])),
				TimetableType: timetableType, TrainType: strings.TrimSpace(string(line[24:25])),
				MinimumFare: minimumFareMarker == "Y",
			})
			index := len(value.TimeRestrictions) - 1
			value.TimeRestrictions[index].DateBands = append(value.TimeRestrictions[index].DateBands, pendingTimeDates[key][ruleKey]...)
			value.TimeRestrictions[index].TOCCodes = append(value.TimeRestrictions[index].TOCCodes, pendingTimeTOCs[key][ruleKey]...)
			delete(pendingTimeDates[key], ruleKey)
			delete(pendingTimeTOCs[key], ruleKey)
		case "TD":
			if err := requireLength(26); err != nil {
				return err
			}
			sequence := strings.TrimSpace(string(line[6:10]))
			outRet := strings.TrimSpace(string(line[10:11]))
			key := restrictionKey{code: code, cf: cf}
			ruleKey := sequence + ":" + outRet
			band := ctdf.FareRailDateBand{FromMonthDay: strings.TrimSpace(string(line[11:15])), ToMonthDay: strings.TrimSpace(string(line[15:19])), Days: strings.TrimSpace(string(line[19:26]))}
			value := getRestriction(cf, code)
			if index, exists := timeRules[key][ruleKey]; exists {
				value.TimeRestrictions[index].DateBands = append(value.TimeRestrictions[index].DateBands, band)
			} else {
				if pendingTimeDates[key] == nil {
					pendingTimeDates[key] = map[string][]ctdf.FareRailDateBand{}
				}
				pendingTimeDates[key][ruleKey] = append(pendingTimeDates[key][ruleKey], band)
			}
		case "TT":
			if err := requireLength(13); err != nil {
				return err
			}
			sequence := strings.TrimSpace(string(line[6:10]))
			outRet := strings.TrimSpace(string(line[10:11]))
			key := restrictionKey{code: code, cf: cf}
			ruleKey := sequence + ":" + outRet
			toc := strings.TrimSpace(string(line[11:13]))
			value := getRestriction(cf, code)
			if index, exists := timeRules[key][ruleKey]; exists {
				value.TimeRestrictions[index].TOCCodes = append(value.TimeRestrictions[index].TOCCodes, toc)
			} else {
				if pendingTimeTOCs[key] == nil {
					pendingTimeTOCs[key] = map[string][]string{}
				}
				pendingTimeTOCs[key][ruleKey] = append(pendingTimeTOCs[key][ruleKey], toc)
			}
		case "SR":
			if err := requireLength(15); err != nil {
				return err
			}
			trainUID := strings.TrimSpace(string(line[6:12]))
			outRet := strings.TrimSpace(string(line[12:13]))
			value := getRestriction(cf, code)
			key := restrictionKey{code: code, cf: cf}
			if trainRules[key] == nil {
				trainRules[key] = map[string]int{}
			}
			ruleKey := trainUID + ":" + outRet
			trainRules[key][ruleKey] = len(value.TrainRestrictions)
			value.TrainRestrictions = append(value.TrainRestrictions, ctdf.FareRailTrainRestriction{
				TrainUID: trainUID, OutRet: outRet,
				Quota:   strings.EqualFold(strings.TrimSpace(string(line[13:14])), "Y"),
				Sleeper: strings.EqualFold(strings.TrimSpace(string(line[14:15])), "Y"),
			})
			index := len(value.TrainRestrictions) - 1
			value.TrainRestrictions[index].DateBands = append(value.TrainRestrictions[index].DateBands, pendingTrainDates[key][ruleKey]...)
			delete(pendingTrainDates[key], ruleKey)
		case "SD":
			if err := requireLength(28); err != nil {
				return err
			}
			trainUID := strings.TrimSpace(string(line[6:12]))
			outRet := strings.TrimSpace(string(line[12:13]))
			key := restrictionKey{code: code, cf: cf}
			ruleKey := trainUID + ":" + outRet
			band := ctdf.FareRailDateBand{FromMonthDay: strings.TrimSpace(string(line[13:17])), ToMonthDay: strings.TrimSpace(string(line[17:21])), Days: strings.TrimSpace(string(line[21:28]))}
			value := getRestriction(cf, code)
			if index, exists := trainRules[key][ruleKey]; exists {
				value.TrainRestrictions[index].DateBands = append(value.TrainRestrictions[index].DateBands, band)
			} else {
				if pendingTrainDates[key] == nil {
					pendingTrainDates[key] = map[string][]ctdf.FareRailDateBand{}
				}
				pendingTrainDates[key][ruleKey] = append(pendingTrainDates[key][ruleKey], band)
			}
		case "CA":
			if err := requireLength(29); err != nil {
				return err
			}
			calendar := ctdf.FareRailCalendar{
				CFMarker: cf,
				Type:     strings.TrimSpace(string(line[7:8])), RouteCode: strings.TrimSpace(string(line[8:13])), Country: strings.TrimSpace(string(line[13:14])),
				FromMonthDay: strings.TrimSpace(string(line[14:18])), ToMonthDay: strings.TrimSpace(string(line[18:22])), Days: strings.TrimSpace(string(line[22:29])),
			}
			ticketCode := strings.TrimSpace(string(line[4:7]))
			if ticketCode == "" {
				return errors.New("RST CA record has no ticket code")
			}
			catalog.calendars[ticketCode] = append(catalog.calendars[ticketCode], calendar)
		case "TP", "TE", "SQ", "SP", "SE":
			if code != "" {
				value := getRestriction(cf, code)
				value.Unsupported = append(value.Unsupported, "RSPS5045 "+typeCode+" record")
			}
		case "RR", "EC":
			// Railcard prohibitions and exception text do not alter the base adult
			// fare. A requested railcard quote is kept partial by the planner.
		default:
			return fmt.Errorf("unsupported RST record type %q at line %d", typeCode, lineNumber)
		}
		return nil
	})
	if err != nil {
		return catalog, err
	}
	for key, pending := range pendingTimeDates {
		for ruleKey := range pending {
			return catalog, fmt.Errorf("RST TD record has no matching TR record for restriction %s sequence %s", key.code, strings.SplitN(ruleKey, ":", 2)[0])
		}
	}
	for key, pending := range pendingTimeTOCs {
		for ruleKey := range pending {
			return catalog, fmt.Errorf("RST TT record has no matching TR record for restriction %s sequence %s", key.code, strings.SplitN(ruleKey, ":", 2)[0])
		}
	}
	for key, pending := range pendingTrainDates {
		for ruleKey := range pending {
			return catalog, fmt.Errorf("RST SD record has no matching SR record for restriction %s train %s", key.code, strings.SplitN(ruleKey, ":", 2)[0])
		}
	}
	for key, value := range builders {
		period := periods[key.cf]
		value.ValidFrom, value.ValidTo = period.from, period.to
		if value.Description == "" {
			value.Unsupported = append(value.Unsupported, "restriction header unavailable")
		}
		value.Unsupported = uniqueStringValues(value.Unsupported)
		catalog.restrictions[key.code] = append(catalog.restrictions[key.code], *value)
	}
	for ticketCode, calendars := range catalog.calendars {
		for index := range calendars {
			period := periods[calendars[index].CFMarker]
			calendars[index].ValidFrom, calendars[index].ValidTo = period.from, period.to
		}
		catalog.calendars[ticketCode] = calendars
	}
	return catalog, nil
}

func parseRailClock(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("HHMM time is blank")
	}
	if len(value) != 4 {
		return 0, fmt.Errorf("invalid HHMM time %q", value)
	}
	hour, hourErr := strconv.Atoi(value[:2])
	minute, minuteErr := strconv.Atoi(value[2:])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid HHMM time %q", value)
	}
	return hour*60 + minute, nil
}

func uniqueStringValues(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
