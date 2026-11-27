package netex

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

// scheduleData contains the timetable objects collected while the document is
// streamed. Objects remain as small XML trees because NeTEx permits references
// to be declared in a different frame or file from the object using them.
type scheduleData struct {
	operators             map[string]*xmlNode
	networks              map[string]*xmlNode
	lines                 map[string]*xmlNode
	routes                map[string]*xmlNode
	patterns              map[string]*xmlNode
	journeys              map[string]*xmlNode
	dayTypes              map[string]*xmlNode
	operatingDays         map[string]*xmlNode
	operatingPeriods      map[string]*xmlNode
	assignments           []*xmlNode
	assignmentsByDayType  map[string][]*xmlNode
	destinations          map[string]string
	timezone              string
	unsupportedScheduling []string
}

func (d *scheduleData) ensureMaps() {
	if d.operators == nil {
		d.operators = map[string]*xmlNode{}
	}
	if d.networks == nil {
		d.networks = map[string]*xmlNode{}
	}
	if d.lines == nil {
		d.lines = map[string]*xmlNode{}
	}
	if d.routes == nil {
		d.routes = map[string]*xmlNode{}
	}
	if d.patterns == nil {
		d.patterns = map[string]*xmlNode{}
	}
	if d.journeys == nil {
		d.journeys = map[string]*xmlNode{}
	}
	if d.dayTypes == nil {
		d.dayTypes = map[string]*xmlNode{}
	}
	if d.operatingDays == nil {
		d.operatingDays = map[string]*xmlNode{}
	}
	if d.operatingPeriods == nil {
		d.operatingPeriods = map[string]*xmlNode{}
	}
	if d.assignmentsByDayType == nil {
		d.assignmentsByDayType = map[string][]*xmlNode{}
	}
	if d.destinations == nil {
		d.destinations = map[string]string{}
	}
}

// decodeScheduleElement consumes an entire recognized object and indexes it.
// A false return leaves the token stream untouched for the stop importer.
func (d *scheduleData) decodeScheduleElement(decoder *xml.Decoder, start xml.StartElement) (bool, error) {
	d.ensureMaps()
	kind := start.Name.Local
	switch kind {
	case "Operator", "Network", "Line", "Route", "JourneyPattern", "ServiceJourneyPattern", "ServiceJourney", "DayType", "OperatingDay", "OperatingPeriod", "DayTypeAssignment", "DestinationDisplay", "DefaultLocale", "TemplateServiceJourney", "TemplateVehicleJourney", "DatedServiceJourney", "JourneyFrequencyGroup", "FrequencyGroup", "HeadwayJourneyGroup", "RhythmicalJourneyGroup":
	default:
		return false, nil
	}
	node := &xmlNode{}
	if err := node.UnmarshalXML(decoder, start); err != nil {
		return true, err
	}
	id := nodeAttr(node, "id")
	switch kind {
	case "Operator":
		if id != "" {
			d.operators[id] = node
		}
	case "Network":
		if id != "" {
			d.networks[id] = node
		}
	case "Line":
		if id != "" {
			d.lines[id] = node
		}
	case "Route":
		if id != "" {
			d.routes[id] = node
		}
	case "JourneyPattern", "ServiceJourneyPattern":
		if id != "" {
			d.patterns[id] = node
		}
	case "ServiceJourney":
		if id != "" {
			d.journeys[id] = node
		}
	case "DayType":
		if id != "" {
			d.dayTypes[id] = node
		}
	case "OperatingDay":
		if id != "" {
			d.operatingDays[id] = node
		}
	case "OperatingPeriod":
		if id != "" {
			d.operatingPeriods[id] = node
		}
	case "DayTypeAssignment":
		d.assignments = append(d.assignments, node)
		if dayTypeRef := firstRefValue(node, "DayTypeRef"); dayTypeRef != "" {
			d.assignmentsByDayType[dayTypeRef] = append(d.assignmentsByDayType[dayTypeRef], node)
		}
	case "DestinationDisplay":
		if id != "" {
			d.destinations[id] = firstNonEmpty(nodeText(node, "FrontText"), nodeText(node, "Name"))
		}
	case "DefaultLocale":
		d.timezone = firstNonEmpty(nodeText(node, "TimeZone"), nodeText(node, "Timezone"), nodeText(node, "TimeZoneName"))
	case "TemplateServiceJourney", "TemplateVehicleJourney", "DatedServiceJourney", "JourneyFrequencyGroup", "FrequencyGroup", "HeadwayJourneyGroup", "RhythmicalJourneyGroup":
		d.unsupportedScheduling = append(d.unsupportedScheduling, kind+" "+firstNonEmpty(id, "(no id)"))
	}
	return true, nil
}

type scheduleAvailability struct {
	match, weekdays, exclude []ctdf.AvailabilityRule
	resolvable               bool
}

func (d *scheduleData) availability(dayTypeRefs []string) (*ctdf.Availability, error) {
	byDayType := map[string]scheduleAvailability{}
	for _, id := range dayTypeRefs {
		dayType := d.dayTypes[id]
		if dayType == nil {
			return nil, fmt.Errorf("missing DayType %q", id)
		}
		entry := scheduleAvailability{}
		// DayType properties are optional. Only understood weekday properties
		// can make a type recurring; unknown properties are not treated as daily.
		for _, property := range findNodes(dayType, "PropertyOfDay") {
			for _, value := range findNodes(property, "DaysOfWeek") {
				weekdays := []string{strings.TrimSpace(value.Text)}
				if len(value.Children) > 0 {
					weekdays = weekdays[:0]
					for _, day := range value.Children {
						weekdays = append(weekdays, strings.TrimSpace(day.Text))
					}
				}
				for _, day := range weekdays {
					weekday, err := netexWeekday(day)
					if err != nil {
						return nil, fmt.Errorf("DayType %q: %w", id, err)
					}
					entry.weekdays = append(entry.weekdays, ctdf.AvailabilityRule{Type: ctdf.AvailabilityDayOfWeek, Value: weekday})
				}
			}
		}
		byDayType[id] = entry
	}
	for _, dayRef := range dayTypeRefs {
		entry := byDayType[dayRef]
		for _, assignment := range d.assignmentsByDayType[dayRef] {
			available, err := netexBoolAttribute(assignment, "isAvailable", true)
			childValue := firstNonEmpty(nodeText(assignment, "IsAvailable"), nodeText(assignment, "isAvailable"))
			if childValue != "" {
				available, err = parseNetexBool(childValue, true)
			}
			if err != nil {
				return nil, fmt.Errorf("DayTypeAssignment %q: %w", nodeAttr(assignment, "id"), err)
			}
			var rule ctdf.AvailabilityRule
			date := nodeText(assignment, "Date")
			if date == "" {
				date = nodeText(assignment, "CalendarDate")
			}
			if date != "" {
				parsed, err := parseNetexDate(date)
				if err != nil {
					return nil, fmt.Errorf("DayTypeAssignment %q date: %w", nodeAttr(assignment, "id"), err)
				}
				rule = ctdf.AvailabilityRule{Type: ctdf.AvailabilityDate, Value: parsed.Format(ctdf.YearMonthDayFormat)}
				entry.resolvable = true
			} else if periodRef := firstRefValue(assignment, "OperatingPeriodRef"); periodRef != "" {
				period := d.operatingPeriods[periodRef]
				if period == nil {
					return nil, fmt.Errorf("DayTypeAssignment %q references missing OperatingPeriod %q", nodeAttr(assignment, "id"), periodRef)
				}
				start, end, err := operatingPeriodBounds(period)
				if err != nil {
					return nil, fmt.Errorf("OperatingPeriod %q: %w", periodRef, err)
				}
				if start.IsZero() && end.IsZero() {
					continue
				}
				rule = ctdf.AvailabilityRule{Type: ctdf.AvailabilityDateRange, Value: fmt.Sprintf("%s:%s", formatOptionalDate(start), formatOptionalDate(end))}
				entry.resolvable = true
			} else if dayRefValue := firstRefValue(assignment, "OperatingDayRef"); dayRefValue != "" {
				day := d.operatingDays[dayRefValue]
				if day == nil {
					return nil, fmt.Errorf("DayTypeAssignment %q references missing OperatingDay %q", nodeAttr(assignment, "id"), dayRefValue)
				}
				value := firstNonEmpty(nodeText(day, "CalendarDate"), nodeText(day, "Date"))
				parsed, err := parseNetexDate(value)
				if err != nil {
					return nil, fmt.Errorf("OperatingDay %q date: %w", dayRefValue, err)
				}
				rule = ctdf.AvailabilityRule{Type: ctdf.AvailabilityDate, Value: parsed.Format(ctdf.YearMonthDayFormat)}
				entry.resolvable = true
			} else {
				return nil, fmt.Errorf("DayTypeAssignment %q has no supported Date, OperatingDayRef or OperatingPeriodRef", nodeAttr(assignment, "id"))
			}
			if available {
				entry.match = append(entry.match, rule)
			} else {
				entry.exclude = append(entry.exclude, rule)
			}
		}
		byDayType[dayRef] = entry
	}
	availability := &ctdf.Availability{}
	for _, id := range dayTypeRefs {
		entry := byDayType[id]
		if !entry.resolvable || (len(entry.match) == 0 && len(entry.weekdays) == 0) {
			return nil, fmt.Errorf("DayType %q has no resolvable positive calendar assignments", id)
		}
		rules, err := resolveDayTypeCalendar(entry)
		if err != nil {
			return nil, fmt.Errorf("DayType %q: %w", id, err)
		}
		availability.Match = append(availability.Match, rules...)
	}
	if len(availability.Match) == 0 {
		return nil, fmt.Errorf("journey has no resolvable calendar")
	}
	return availability, nil
}

func resolveDayTypeCalendar(entry scheduleAvailability) ([]ctdf.AvailabilityRule, error) {
	if len(entry.match) == 0 {
		if len(entry.exclude) > 0 {
			return nil, fmt.Errorf("weekly calendar with exclusions is unsupported without bounded positive periods")
		}
		return append([]ctdf.AvailabilityRule(nil), entry.weekdays...), nil
	}
	positive := map[string]time.Time{}
	for _, rule := range entry.match {
		dates, err := calendarRuleDates(rule)
		if err != nil {
			return nil, err
		}
		for _, date := range dates {
			if len(entry.weekdays) == 0 || dateMatchesWeekday(date, entry.weekdays) {
				positive[date.Format(ctdf.YearMonthDayFormat)] = date
			}
		}
	}
	for _, rule := range entry.exclude {
		dates, err := calendarRuleDates(rule)
		if err != nil {
			return nil, err
		}
		for _, date := range dates {
			if len(entry.weekdays) == 0 || dateMatchesWeekday(date, entry.weekdays) {
				delete(positive, date.Format(ctdf.YearMonthDayFormat))
			}
		}
	}
	if len(positive) == 0 {
		return nil, fmt.Errorf("calendar assignments resolve to no operating dates")
	}
	return compressCalendarDates(positive), nil
}

func calendarRuleDates(rule ctdf.AvailabilityRule) ([]time.Time, error) {
	if rule.Type == ctdf.AvailabilityDate {
		date, err := time.Parse(ctdf.YearMonthDayFormat, rule.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid assignment date %q", rule.Value)
		}
		return []time.Time{date}, nil
	}
	if rule.Type != ctdf.AvailabilityDateRange {
		return nil, fmt.Errorf("unsupported calendar rule %q", rule.Type)
	}
	parts := strings.Split(rule.Value, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("open-ended OperatingPeriod %q cannot be represented safely", rule.Value)
	}
	start, err := time.Parse(ctdf.YearMonthDayFormat, parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid period start %q", parts[0])
	}
	end, err := time.Parse(ctdf.YearMonthDayFormat, parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid period end %q", parts[1])
	}
	if end.Before(start) {
		return nil, fmt.Errorf("period end %s precedes start %s", parts[1], parts[0])
	}
	if end.Sub(start) > 3650*24*time.Hour {
		return nil, fmt.Errorf("OperatingPeriod exceeds supported ten-year calendar span")
	}
	dates := make([]time.Time, 0, int(end.Sub(start).Hours()/24)+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		dates = append(dates, date)
	}
	return dates, nil
}

func dateMatchesWeekday(date time.Time, weekdays []ctdf.AvailabilityRule) bool {
	for _, weekday := range weekdays {
		if weekday.Value == date.Weekday().String() {
			return true
		}
	}
	return false
}

func compressCalendarDates(dates map[string]time.Time) []ctdf.AvailabilityRule {
	ordered := make([]time.Time, 0, len(dates))
	for _, date := range dates {
		ordered = append(ordered, date)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	rules := make([]ctdf.AvailabilityRule, 0)
	for start, index := 0, 0; index < len(ordered); index++ {
		if index+1 < len(ordered) && ordered[index+1].Equal(ordered[index].AddDate(0, 0, 1)) {
			continue
		}
		if start == index {
			rules = append(rules, ctdf.AvailabilityRule{Type: ctdf.AvailabilityDate, Value: ordered[start].Format(ctdf.YearMonthDayFormat)})
		} else {
			rules = append(rules, ctdf.AvailabilityRule{Type: ctdf.AvailabilityDateRange, Value: ordered[start].Format(ctdf.YearMonthDayFormat) + ":" + ordered[index].Format(ctdf.YearMonthDayFormat)})
		}
		start = index + 1
	}
	return rules
}

func (s *Schedule) convertTimetable(dataset datasets.DataSet, source *ctdf.DataSourceReference, now time.Time) ([]*ctdf.Operator, []*ctdf.Service, []*ctdf.Journey, error) {
	d := &s.scheduleData
	d.ensureMaps()
	if len(d.unsupportedScheduling) > 0 {
		return nil, nil, nil, fmt.Errorf("unsupported NeTEx scheduling variants: %s", strings.Join(uniqueStrings(d.unsupportedScheduling), ", "))
	}
	locationName := strings.TrimSpace(d.timezone)
	if configured := strings.TrimSpace(dataset.CustomConfig["timezone"]); configured != "" {
		locationName = configured
	}
	if locationName == "" && len(d.journeys) > 0 {
		return nil, nil, nil, fmt.Errorf("NeTEx timetable has journeys but no timezone; configure CustomConfig timezone or provide DefaultLocale/TimeZone")
	}
	if locationName == "" {
		locationName = "UTC"
	}
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load NeTEx schedule timezone %q: %w", locationName, err)
	}

	operatorRefs := map[string]string{}
	operatorsByID := map[string]*ctdf.Operator{}
	operatorIDs := make([]string, 0, len(d.operators))
	for ref := range d.operators {
		operatorIDs = append(operatorIDs, ref)
	}
	for _, ref := range operatorIDs {
		node := d.operators[ref]
		name := firstNonEmpty(nodeText(node, "Name"), nodeText(node, "ShortName"), netexReferenceLocalID(ref))
		id := netexID(dataset.Identifier, "operator", ref)
		other := strings.TrimSpace(nodeText(node, "ShortName"))
		operator := &ctdf.Operator{PrimaryIdentifier: id, CreationDateTime: now, ModificationDateTime: now, DataSource: source, PrimaryName: name}
		if other != "" && other != name {
			operator.OtherNames = []string{other}
		}
		operatorsByID[id] = operator
		operatorRefs[ref] = id
		if alias := firstNonEmpty(dataset.OperatorAliases[ref], dataset.OperatorAliases[id]); alias != "" {
			operatorRefs[ref] = alias
		}
	}

	servicesByID := map[string]*ctdf.Service{}
	for ref, line := range d.lines {
		operatorSourceRef := firstRefValue(line, "OperatorRef")
		operatorID := operatorRefs[operatorSourceRef]
		if operatorID == "" && operatorSourceRef != "" {
			return nil, nil, nil, fmt.Errorf("Line %q references missing Operator %q", ref, operatorSourceRef)
		}
		if utilIgnoredOperator(dataset, operatorID) {
			continue
		}
		name := firstNonEmpty(nodeText(line, "PublicCode"), nodeText(line, "ShortName"), nodeText(line, "Name"))
		serviceID := netexID(dataset.Identifier, "service", ref)
		mode, err := netexTransportMode(line)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Line %q: %w", ref, err)
		}
		service := &ctdf.Service{PrimaryIdentifier: serviceID, OtherIdentifiers: []string{ref}, CreationDateTime: now, ModificationDateTime: now, DataSource: source, ServiceName: name, Description: nodeText(line, "Name"), OperatorRef: operatorID, TransportType: mode}
		service.NetworkRef = netexNetworkRef(dataset.Identifier, firstRefValue(line, "NetworkRef"))
		presentation := firstNode(line, "Presentation")
		if colour := strings.TrimSpace(nodeText(presentation, "Colour")); colour != "" {
			service.BrandColour = "#" + strings.TrimPrefix(colour, "#")
		}
		if colour := strings.TrimSpace(nodeText(presentation, "TextColour")); colour != "" {
			service.SecondaryBrandColour = "#" + strings.TrimPrefix(colour, "#")
		}
		servicesByID[ref] = service
	}

	journeyRefs := make([]string, 0, len(d.journeys))
	for ref := range d.journeys {
		journeyRefs = append(journeyRefs, ref)
	}
	journeys := make([]*ctdf.Journey, 0, len(journeyRefs))
	availabilityByKey := make(map[string]*ctdf.Availability)
	for _, journeyRef := range journeyRefs {
		journeyNode := d.journeys[journeyRef]
		patternRef := firstRefValue(journeyNode, "JourneyPatternRef")
		if patternRef == "" {
			patternRef = firstRefValue(journeyNode, "ServiceJourneyPatternRef")
		}
		pattern := d.patterns[patternRef]
		if pattern == nil {
			return nil, nil, nil, fmt.Errorf("ServiceJourney %q references missing JourneyPattern %q", journeyRef, patternRef)
		}
		routeRef := firstRefValue(pattern, "RouteRef")
		lineRef := ""
		if routeRef != "" {
			route := d.routes[routeRef]
			if route == nil {
				return nil, nil, nil, fmt.Errorf("JourneyPattern %q references missing Route %q", patternRef, routeRef)
			}
			lineRef = firstRefValue(route, "LineRef")
		}
		if lineRef == "" {
			lineRef = firstRefValue(pattern, "LineRef")
		}
		service := servicesByID[lineRef]
		if service == nil && d.lines[lineRef] != nil {
			continue
		}
		if service == nil {
			return nil, nil, nil, fmt.Errorf("JourneyPattern %q has no imported Line %q", patternRef, lineRef)
		}
		dayRefs := referenceValues(journeyNode, "DayTypeRef")
		if len(dayRefs) == 0 {
			return nil, nil, nil, fmt.Errorf("ServiceJourney %q has no DayTypeRef", journeyRef)
		}
		calendarKey := strings.Join(dayRefs, "\x00")
		availability := availabilityByKey[calendarKey]
		if availability == nil {
			availability, err = d.availability(dayRefs)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("ServiceJourney %q calendar: %w", journeyRef, err)
			}
			availabilityByKey[calendarKey] = availability
		}
		calls, err := s.journeyCalls(dataset, journeyNode, pattern, location)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("ServiceJourney %q: %w", journeyRef, err)
		}
		if len(calls) < 2 {
			return nil, nil, nil, fmt.Errorf("ServiceJourney %q has fewer than two calls", journeyRef)
		}
		journey := &ctdf.Journey{PrimaryIdentifier: netexID(dataset.Identifier, "journey", journeyRef), OtherIdentifiers: map[string]string{"NeTEx": journeyRef}, CreationDateTime: now, ModificationDateTime: now, DataSource: source, ServiceRef: service.PrimaryIdentifier, OperatorRef: service.OperatorRef, Direction: firstNonEmpty(nodeText(pattern, "Name"), nodeText(journeyNode, "Direction")), ShortName: nodeText(journeyNode, "Name"), DepartureTimezone: locationName, DestinationDisplay: d.patternDestination(pattern), DepartureTime: calls[0].Departure, Availability: availability, Calls: calls}
		journey.RefreshStopRefs()
		if err := journey.ValidatePath(); err != nil {
			return nil, nil, nil, fmt.Errorf("validate NeTEx journey %q: %w", journeyRef, err)
		}
		service.Routes = appendRouteUnique(service.Routes, ctdf.Route{Origin: callName(calls[0]), Destination: callName(calls[len(calls)-1]), Description: journey.Direction})
		journeys = append(journeys, journey)
	}
	operators := make([]*ctdf.Operator, 0, len(operatorsByID))
	for _, operator := range operatorsByID {
		operators = append(operators, operator)
	}
	services := make([]*ctdf.Service, 0, len(servicesByID))
	for _, service := range servicesByID {
		services = append(services, service)
	}
	return operators, services, journeys, nil
}

func (s *Schedule) journeyCalls(dataset datasets.DataSet, journey, pattern *xmlNode, location *time.Location) ([]ctdf.JourneyCall, error) {
	points := orderedPatternPoints(pattern)
	passingTimes := findNodes(journey, "TimetabledPassingTime")
	if len(points) != len(passingTimes) {
		return nil, fmt.Errorf("has %d TimetabledPassingTime entries for %d pattern points", len(passingTimes), len(points))
	}
	passingByPoint := make(map[string]*xmlNode, len(passingTimes))
	for _, pass := range passingTimes {
		pointRef := firstRefValue(pass, "PointInJourneyPatternRef")
		if pointRef == "" {
			pointRef = firstRefValue(pass, "StopPointInJourneyPatternRef")
		}
		if pointRef == "" {
			pointRef = firstRefValue(pass, "PointInLinkSequenceRef")
		}
		if pointRef == "" {
			return nil, fmt.Errorf("TimetabledPassingTime %q has no supported point reference", nodeAttr(pass, "id"))
		}
		if passingByPoint[pointRef] != nil {
			return nil, fmt.Errorf("multiple TimetabledPassingTime entries refer to pattern point %q", pointRef)
		}
		passingByPoint[pointRef] = pass
	}
	calls := make([]ctdf.JourneyCall, len(points))
	for index, point := range points {
		pointID := nodeAttr(point, "id")
		pass := passingByPoint[pointID]
		if pass == nil {
			return nil, fmt.Errorf("pattern point %q has no TimetabledPassingTime", pointID)
		}
		stopRef := firstRefValue(point, "ScheduledStopPointRef")
		if stopRef == "" {
			stopRef = firstRefValue(point, "StopPointRef")
		}
		if stopRef == "" {
			return nil, fmt.Errorf("pattern point %q has no supported stop reference", pointID)
		}
		id, name, err := s.resolveStop(dataset, stopRef)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, fmt.Errorf("pattern point %q references unresolved stop %q", pointID, stopRef)
		}
		arrival, err := netexPassingTime(pass, "ArrivalTime", "ArrivalDayOffset", location)
		if err != nil {
			return nil, err
		}
		if index > 0 && !arrival.IsZero() {
			arrival, err = netexAdvanceClock(arrival, calls[index-1].Departure, pass, "ArrivalDayOffset")
			if err != nil {
				return nil, err
			}
		}
		departure, err := netexPassingTime(pass, "DepartureTime", "DepartureDayOffset", location)
		if err != nil {
			return nil, err
		}
		if !arrival.IsZero() && !departure.IsZero() && departure.Before(arrival) {
			departure, err = netexAdvanceClock(departure, arrival, pass, "DepartureDayOffset")
			if err != nil {
				return nil, err
			}
		}
		if arrival.IsZero() {
			arrival = departure
		}
		if departure.IsZero() {
			departure = arrival
		}
		if arrival.IsZero() {
			return nil, fmt.Errorf("passing time for point %q has neither arrival nor departure time", pointID)
		}
		activities := make([]ctdf.JourneyPathItemActivity, 0, 2)
		boarding := nodeText(point, "ForBoarding")
		alighting := nodeText(point, "ForAlighting")
		canBoard, err := parseNetexBool(boarding, true)
		if err != nil {
			return nil, fmt.Errorf("point %q ForBoarding: %w", pointID, err)
		}
		canAlight, err := parseNetexBool(alighting, true)
		if err != nil {
			return nil, fmt.Errorf("point %q ForAlighting: %w", pointID, err)
		}
		if canBoard {
			activities = append(activities, ctdf.JourneyPathItemActivityPickup)
		}
		if canAlight {
			activities = append(activities, ctdf.JourneyPathItemActivitySetdown)
		}
		if len(activities) == 0 {
			activities = []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPass}
		}
		calls[index] = ctdf.JourneyCall{StopRef: id, Stop: &ctdf.Stop{PrimaryIdentifier: id, PrimaryName: name}, Arrival: arrival, Departure: departure, Activity: activities, Start: index == 0, End: index == len(points)-1}
		if index > 0 {
			calls[index-1].Outgoing = &ctdf.JourneyLeg{DestinationDisplay: s.scheduleData.patternDestination(pattern)}
		}
	}
	return calls, nil
}

func netexAdvanceClock(value, previous time.Time, node *xmlNode, offsetName string) (time.Time, error) {
	if value.IsZero() || previous.IsZero() || !value.Before(previous) {
		return value, nil
	}
	if nodeText(node, offsetName) != "" {
		return time.Time{}, fmt.Errorf("%s produces a time before the preceding passing time", offsetName)
	}
	for value.Before(previous) {
		value = value.AddDate(0, 0, 1)
	}
	return value, nil
}

func (d *scheduleData) patternDestination(pattern *xmlNode) string {
	if ref := firstRefValue(pattern, "DestinationDisplayRef"); ref != "" {
		if value := d.destinations[ref]; value != "" {
			return value
		}
	}
	return firstNonEmpty(nodeText(pattern, "DestinationDisplay"), nodeText(pattern, "Name"))
}

func orderedPatternPoints(pattern *xmlNode) []*xmlNode {
	points := make([]*xmlNode, 0)
	for _, container := range pattern.Children {
		if container.Name.Local != "pointsInSequence" && container.Name.Local != "PointsInSequence" {
			continue
		}
		for _, point := range container.Children {
			switch point.Name.Local {
			case "StopPointInJourneyPattern", "ScheduledStopPointInJourneyPattern", "PointInJourneyPattern":
				points = append(points, point)
			}
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return sequenceOrder(points[i]) < sequenceOrder(points[j]) })
	return points
}

func netexPassingTime(node *xmlNode, timeName, offsetName string, location *time.Location) (time.Time, error) {
	clock := strings.TrimSpace(nodeText(node, timeName))
	if clock == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse("15:04:05", clock)
	if err != nil {
		parsed, err = time.Parse("15:04", clock)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s %q: %w", timeName, clock, err)
	}
	dayOffset := 0
	if value := nodeText(node, offsetName); value != "" {
		dayOffset, err = strconv.Atoi(value)
		if err != nil || dayOffset < 0 {
			return time.Time{}, fmt.Errorf("invalid %s %q", offsetName, value)
		}
	}
	_ = location // Wall-clock times are anchored in UTC; DepartureTimezone carries the service zone.
	return time.Date(0, 1, 1+dayOffset, parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), time.UTC), nil
}

func operatingPeriodBounds(node *xmlNode) (time.Time, time.Time, error) {
	startText := firstNonEmpty(nodeText(node, "FromDate"), nodeText(node, "StartDate"))
	endText := firstNonEmpty(nodeText(node, "ToDate"), nodeText(node, "EndDate"))
	start, err := parseOptionalNetexDate(startText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseOptionalNetexDate(endText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end date %s precedes start date %s", endText, startText)
	}
	return start, end, nil
}

func parseOptionalNetexDate(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return parseNetexDate(value)
}
func parseNetexDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			year, month, day := parsed.Date()
			return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid calendar date %q", value)
}
func formatOptionalDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(ctdf.YearMonthDayFormat)
}
func netexBoolAttribute(node *xmlNode, name string, defaultValue bool) (bool, error) {
	value := nodeAttr(node, name)
	if value == "" {
		return defaultValue, nil
	}
	return parseNetexBool(value, defaultValue)
}
func parseNetexBool(value string, defaultValue bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return defaultValue, nil
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", value)
	}
}
func netexWeekday(value string) (string, error) {
	switch strings.ToLower(value) {
	case "monday":
		return "Monday", nil
	case "tuesday":
		return "Tuesday", nil
	case "wednesday":
		return "Wednesday", nil
	case "thursday":
		return "Thursday", nil
	case "friday":
		return "Friday", nil
	case "saturday":
		return "Saturday", nil
	case "sunday":
		return "Sunday", nil
	default:
		return "", fmt.Errorf("unsupported weekday %q", value)
	}
}
func firstRefValue(node *xmlNode, name string) string {
	refs := findNodes(node, name)
	if len(refs) == 0 {
		return ""
	}
	return nodeAttr(refs[0], "ref")
}
func referenceValues(node *xmlNode, name string) []string {
	values := []string{}
	for _, ref := range findNodes(node, name) {
		if value := nodeAttr(ref, "ref"); value != "" {
			values = append(values, value)
		}
	}
	return uniqueStrings(values)
}
func sequenceOrder(node *xmlNode) int {
	value, _ := strconv.Atoi(nodeAttr(node, "order"))
	return value
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
func netexTransportMode(line *xmlNode) (ctdf.TransportType, error) {
	mode := strings.ToLower(strings.TrimSpace(nodeText(line, "TransportMode")))
	for _, node := range findNodes(line, "TransportSubmode") {
		if len(node.Children) > 0 {
			mode = strings.ToLower(node.Children[0].Text)
			if mode == "" {
				mode = strings.ToLower(node.Children[0].Name.Local)
			}
			break
		}
	}
	switch mode {
	case "bus", "coach", "shuttlebus", "airportlinkbus", "nightbus":
		return ctdf.TransportTypeBus, nil
	case "rail", "train", "longdistancetrain", "localtrain", "regionalrail":
		return ctdf.TransportTypeRail, nil
	case "tram", "lighttrain":
		return ctdf.TransportTypeTram, nil
	case "metro", "underground":
		return ctdf.TransportTypeMetro, nil
	case "ferry", "water":
		return ctdf.TransportTypeFerry, nil
	case "cablecar":
		return ctdf.TransportTypeCableCar, nil
	case "funicular":
		return ctdf.TransportTypeFunicular, nil
	case "taxi":
		return ctdf.TransportTypeTaxi, nil
	case "":
		return ctdf.TransportTypeUnknown, nil
	default:
		return "", fmt.Errorf("unsupported TransportMode %q", mode)
	}
}
func netexNetworkRef(datasetID, ref string) string {
	if ref == "" {
		return ""
	}
	return netexID(datasetID, "network", ref)
}
func utilIgnoredOperator(dataset datasets.DataSet, ref string) bool {
	for _, ignored := range dataset.IgnoreObjects.Services.ByOperator {
		if ignored == ref {
			return true
		}
	}
	return false
}
func callName(call ctdf.JourneyCall) string {
	if call.Stop != nil {
		return call.Stop.PrimaryName
	}
	return call.StopRef
}
func appendRouteUnique(routes []ctdf.Route, route ctdf.Route) []ctdf.Route {
	for _, existing := range routes {
		if existing.Origin == route.Origin && existing.Destination == route.Destination && existing.Description == route.Description {
			return routes
		}
	}
	return append(routes, route)
}
