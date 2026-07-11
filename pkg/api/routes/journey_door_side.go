package routes

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
)

type trainDoorSide string

const (
	trainDoorSideLeft    trainDoorSide = "Left"
	trainDoorSideRight   trainDoorSide = "Right"
	trainDoorSideUnknown trainDoorSide = "Unknown"
)

type trainDoorPlatformSource string

const (
	trainDoorPlatformRealtime  trainDoorPlatformSource = "Realtime"
	trainDoorPlatformScheduled trainDoorPlatformSource = "Scheduled"
	trainDoorPlatformUnknown   trainDoorPlatformSource = "Unknown"
)

type journeyStopDoorSideResponse struct {
	JourneyRef      string                  `json:"journey_ref"`
	StopRef         string                  `json:"stop_ref"`
	PreviousStopRef string                  `json:"previous_stop_ref,omitempty"`
	NextStopRef     string                  `json:"next_stop_ref,omitempty"`
	Visit           int                     `json:"visit"`
	Platform        string                  `json:"platform,omitempty"`
	PlatformSource  trainDoorPlatformSource `json:"platform_source"`
	Side            trainDoorSide           `json:"side"`
	Confidence      float64                 `json:"confidence"`
	Reason          string                  `json:"reason"`
	PlatformElement *ctdf.OSMElementRef     `json:"platform_element,omitempty"`
	TrackElement    *ctdf.OSMElementRef     `json:"track_element,omitempty"`
}

type journeyStopVisit struct {
	stopRef           string
	journeyStopIndex  int
	previousStopRef   string
	nextStopRef       string
	scheduledPlatform string
}

func getJourneyStopDoorSide(c *fiber.Ctx) error {
	journeyIdentifier := c.Params("identifier")
	stopIdentifier := c.Params("stop_identifier")
	visitNumber, err := strconv.Atoi(c.Query("visit", "1"))
	if err != nil || visitNumber < 1 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Parameter visit should be a positive integer",
		})
	}

	journey, err := dataaggregator.Lookup[*ctdf.Journey](query.Journey{
		PrimaryIdentifier: journeyIdentifier,
	})
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: stopIdentifier})
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	visit, err := findJourneyStopVisit(journey, stop, visitNumber)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}

	platform := visit.scheduledPlatform
	platformSource := trainDoorPlatformUnknown
	if platform != "" {
		platformSource = trainDoorPlatformScheduled
	}

	realtimeJourney, realtimeErr := realtimestore.FindCurrentForJourney(context.Background(), journey.PrimaryIdentifier)
	if realtimeErr != nil {
		log.Debug().Err(realtimeErr).Str("journey", journey.PrimaryIdentifier).Msg("Could not query realtime platform for train door side")
	} else if realtimePlatform := findRealtimePlatform(realtimeJourney, stop, visit.stopRef, visit.journeyStopIndex); realtimePlatform != "" {
		platform = realtimePlatform
		platformSource = trainDoorPlatformRealtime
	}

	response := journeyStopDoorSideResponse{
		JourneyRef:      journey.PrimaryIdentifier,
		StopRef:         visit.stopRef,
		PreviousStopRef: visit.previousStopRef,
		NextStopRef:     visit.nextStopRef,
		Visit:           visitNumber,
		Platform:        platform,
		PlatformSource:  platformSource,
		Side:            trainDoorSideUnknown,
		Reason:          "Door side could not be calculated",
	}

	if platform == "" {
		response.Reason = "No realtime or scheduled platform is available"
		return c.JSON(response)
	}
	if stop.Location == nil || !validLocation(*stop.Location) {
		response.Reason = "The stop has no usable location"
		return c.JSON(response)
	}

	previousLocation := lookupStopLocation(visit.previousStopRef)
	nextLocation := lookupStopLocation(visit.nextStopRef)
	if previousLocation == nil && nextLocation == nil {
		response.Reason = "Neither the previous nor next stop has a usable location"
		return c.JSON(response)
	}

	osmStop, err := dataaggregator.Lookup[*ctdf.OSMStop](query.OSMStop{Stop: stop})
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	calculation := calculateTrainDoorSide(osmStop, platform, *stop.Location, previousLocation, nextLocation)
	response.Side = calculation.side
	response.Confidence = calculation.confidence
	response.Reason = calculation.reason
	response.PlatformElement = calculation.platformElement
	response.TrackElement = calculation.trackElement

	return c.JSON(response)
}

func findJourneyStopVisit(journey *ctdf.Journey, stop *ctdf.Stop, visitNumber int) (journeyStopVisit, error) {
	if journey == nil || len(journey.Path) == 0 {
		return journeyStopVisit{}, fmt.Errorf("journey has no path")
	}

	stopIDs := make(map[string]struct{}, len(stop.GetAllStopIDs()))
	for _, stopID := range stop.GetAllStopIDs() {
		stopIDs[stopID] = struct{}{}
	}

	refs := make([]string, 0, len(journey.Path)+1)
	refs = append(refs, journey.Path[0].OriginStopRef)
	for _, pathItem := range journey.Path {
		refs = append(refs, pathItem.DestinationStopRef)
	}

	matchingVisit := 0
	for index, stopRef := range refs {
		if _, matches := stopIDs[stopRef]; !matches {
			continue
		}

		matchingVisit++
		if matchingVisit != visitNumber {
			continue
		}

		visit := journeyStopVisit{stopRef: stopRef, journeyStopIndex: index}
		if index > 0 {
			visit.previousStopRef = refs[index-1]
			visit.scheduledPlatform = journey.Path[index-1].DestinationPlatform
		}
		if index < len(journey.Path) {
			visit.nextStopRef = refs[index+1]
			if visit.scheduledPlatform == "" {
				visit.scheduledPlatform = journey.Path[index].OriginPlatform
			}
		}
		return visit, nil
	}

	return journeyStopVisit{}, fmt.Errorf("stop is not visit %d in this journey", visitNumber)
}

func findRealtimePlatform(realtimeJourney *ctdf.RealtimeJourney, stop *ctdf.Stop, journeyStopRef string, journeyStopIndex int) string {
	if realtimeJourney == nil {
		return ""
	}

	stopIDs := make(map[string]struct{}, len(stop.GetAllStopIDs())+1)
	stopIDs[journeyStopRef] = struct{}{}
	for _, stopID := range stop.GetAllStopIDs() {
		stopIDs[stopID] = struct{}{}
	}

	for stopID := range stopIDs {
		if realtimeStop := realtimeJourney.RealtimeStop(stopID, journeyStopIndex); realtimeStop != nil && strings.TrimSpace(realtimeStop.Platform) != "" {
			return strings.TrimSpace(realtimeStop.Platform)
		}
	}
	for _, realtimeStop := range realtimeJourney.Stops {
		if realtimeStop == nil {
			continue
		}
		if _, matches := stopIDs[realtimeStop.StopRef]; matches && realtimeStop.JourneyStopIndex == journeyStopIndex && strings.TrimSpace(realtimeStop.Platform) != "" {
			return strings.TrimSpace(realtimeStop.Platform)
		}
	}

	return ""
}

func lookupStopLocation(stopRef string) *ctdf.Location {
	if stopRef == "" {
		return nil
	}
	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: stopRef})
	if err != nil || stop == nil || stop.Location == nil || !validLocation(*stop.Location) {
		return nil
	}
	return stop.Location
}

type doorSideCalculation struct {
	side            trainDoorSide
	confidence      float64
	reason          string
	platformElement *ctdf.OSMElementRef
	trackElement    *ctdf.OSMElementRef
}

type projectedPoint struct {
	x float64
	y float64
}

type trackMatch struct {
	trackFeatureIndex    int
	platformFeatureIndex int
	distance             float64
	trackPoint           projectedPoint
	platformPoint        projectedPoint
	segmentStart         projectedPoint
	segmentEnd           projectedPoint
	trackStart           projectedPoint
	trackEnd             projectedPoint
}

func calculateTrainDoorSide(osmStop *ctdf.OSMStop, platform string, stopLocation ctdf.Location, previousLocation *ctdf.Location, nextLocation *ctdf.Location) doorSideCalculation {
	unknown := doorSideCalculation{side: trainDoorSideUnknown, reason: "Door side could not be calculated"}
	if osmStop == nil {
		unknown.reason = "No OSM stop data is available"
		return unknown
	}

	platformIndexes := matchingPlatformFeatureIndexes(osmStop.Features, platform, osmStop.TransportTypes)
	if len(platformIndexes) == 0 {
		unknown.reason = "The platform could not be matched to an OSM platform"
		return unknown
	}

	trackIndexes := make([]int, 0, len(osmStop.Features))
	for index, feature := range osmStop.Features {
		if feature.Type == ctdf.OSMStopFeatureTypeTrack && len(feature.Geometry) >= 2 && featureMatchesTransport(feature, osmStop.TransportTypes) {
			trackIndexes = append(trackIndexes, index)
		}
	}
	// Sparse OSM records do not always tag the mode. In that case, retain the
	// unfiltered tracks rather than failing a calculation that was possible before.
	if len(trackIndexes) == 0 {
		for index, feature := range osmStop.Features {
			if feature.Type == ctdf.OSMStopFeatureTypeTrack && len(feature.Geometry) >= 2 {
				trackIndexes = append(trackIndexes, index)
			}
		}
	}
	if len(trackIndexes) == 0 {
		unknown.reason = "No OSM rail track geometry is available at the stop"
		return unknown
	}
	trackIndexes = preferPlatformLineTracks(osmStop.Features, platformIndexes, trackIndexes)

	referenceLatitude := stopLocation.Coordinates[1]
	direction, ok := journeyDirection(stopLocation, previousLocation, nextLocation, referenceLatitude)
	if !ok {
		unknown.reason = "Journey direction could not be determined from adjacent stops"
		return unknown
	}
	trackWasIdentifiedByStopPosition := false
	if stopPositionTrackIndex, found := trackIndexForPlatformStopPosition(osmStop.Features, platform, osmStop.TransportTypes, trackIndexes, referenceLatitude); found {
		trackIndexes = []int{stopPositionTrackIndex}
		trackWasIdentifiedByStopPosition = true
	}

	matches := make([]trackMatch, 0, len(platformIndexes)*len(trackIndexes))
	for _, platformIndex := range platformIndexes {
		platformPoints := platformProjectedPoints(osmStop.Features[platformIndex], referenceLatitude)
		if len(platformPoints) == 0 {
			continue
		}
		for _, trackIndex := range trackIndexes {
			trackPoints := featureProjectedPoints(osmStop.Features[trackIndex], referenceLatitude)
			if match, found := closestTrackMatch(platformPoints, trackPoints); found {
				match.trackFeatureIndex = trackIndex
				match.platformFeatureIndex = platformIndex
				matches = append(matches, match)
			}
		}
	}
	if len(matches) == 0 {
		unknown.reason = "The matched OSM platform has no usable geometry"
		return unknown
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].distance < matches[j].distance })
	best := matches[0]
	trackWasIdentifiedByDirection := false
	if platformHasDirectionHint(osmStop.Features[best.platformFeatureIndex], platform) {
		if directionMatch, found := directionAlignedTrackMatch(competingTrackMatches(matches, best), direction); found {
			best = directionMatch
			trackWasIdentifiedByDirection = true
		}
	}
	platformFeature := osmStop.Features[best.platformFeatureIndex]
	platformElement := platformFeature.Element
	trackElement := osmStop.Features[best.trackFeatureIndex].Element
	unknown.platformElement = &platformElement
	unknown.trackElement = &trackElement

	if best.distance > 30 && !trackWasIdentifiedByStopPosition {
		unknown.reason = "The nearest OSM track is too far from the matched platform"
		return unknown
	}
	competingTracksAgree := false
	if !trackWasIdentifiedByStopPosition && !trackWasIdentifiedByDirection && platformFeature.Type != ctdf.OSMStopFeatureTypePlatformEdge {
		competingMatches := competingTrackMatches(matches, best)
		if len(competingMatches) > 1 {
			if !trackMatchesAgreeOnDoorSide(competingMatches, direction) {
				unknown.reason = "The platform is similarly close to tracks on both sides"
				return unknown
			}
			competingTracksAgree = true
		}
	}

	trackVector := projectedPoint{x: best.segmentEnd.x - best.segmentStart.x, y: best.segmentEnd.y - best.segmentStart.y}
	if dot(trackVector, direction) < 0 {
		trackVector.x = -trackVector.x
		trackVector.y = -trackVector.y
	}
	alignment := math.Abs(dot(trackVector, direction) / (vectorLength(trackVector) * vectorLength(direction)))
	if math.IsNaN(alignment) || alignment < 0.15 {
		unknown.reason = "The local OSM track direction conflicts with the journey direction"
		return unknown
	}

	platformVector := projectedPoint{x: best.platformPoint.x - best.trackPoint.x, y: best.platformPoint.y - best.trackPoint.y}
	if vectorLength(platformVector) < 0.25 {
		unknown.reason = "The OSM platform geometry does not identify a side of the track"
		return unknown
	}

	crossProduct := trackVector.x*platformVector.y - trackVector.y*platformVector.x
	if math.Abs(crossProduct) < 0.01 {
		unknown.reason = "The OSM platform geometry is collinear with the track"
		return unknown
	}

	result := doorSideCalculation{
		side:            trainDoorSideRight,
		confidence:      math.Round(math.Min(0.98, 0.72+0.2*alignment)*100) / 100,
		reason:          "Platform side derived from the local OSM track geometry and journey direction",
		platformElement: &platformElement,
		trackElement:    &trackElement,
	}
	if crossProduct > 0 {
		result.side = trainDoorSideLeft
	}
	if platformFeature.Type == ctdf.OSMStopFeatureTypePlatformEdge {
		result.confidence = math.Min(0.98, result.confidence+0.05)
	}
	if competingTracksAgree {
		result.confidence = math.Min(result.confidence, 0.78)
		result.reason = "Multiple nearby OSM tracks imply the same platform side for this journey direction"
	}
	if trackWasIdentifiedByDirection {
		result.confidence = math.Min(result.confidence, 0.65)
		result.reason = "Track selected from directional platform metadata and OSM way orientation; platform side derived from local geometry"
	}
	return result
}

func matchingPlatformFeatureIndexes(features []ctdf.OSMStopFeature, platform string, transportTypes []ctdf.TransportType) []int {
	wanted := platformTokens(platform)
	if len(wanted) == 0 {
		return nil
	}

	edges := []int{}
	platforms := []int{}
	for index, feature := range features {
		if feature.Type != ctdf.OSMStopFeatureTypePlatform && feature.Type != ctdf.OSMStopFeatureTypePlatformEdge {
			continue
		}
		values := []string{feature.Ref, feature.LocalRef, feature.PrimaryName, feature.Tags["ref"], feature.Tags["local_ref"], feature.Tags["name"]}
		if !platformTokenSetsIntersect(wanted, values) {
			continue
		}
		if feature.Type == ctdf.OSMStopFeatureTypePlatformEdge {
			edges = append(edges, index)
		} else {
			platforms = append(platforms, index)
		}
	}
	if len(edges) > 0 {
		return preferTransportFeatures(features, edges, transportTypes)
	}
	return preferTransportFeatures(features, platforms, transportTypes)
}

func preferTransportFeatures(features []ctdf.OSMStopFeature, indexes []int, transportTypes []ctdf.TransportType) []int {
	preferred := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if featureMatchesTransport(features[index], transportTypes) {
			preferred = append(preferred, index)
		}
	}
	if len(preferred) > 0 {
		return preferred
	}
	return indexes
}

func featureMatchesTransport(feature ctdf.OSMStopFeature, transportTypes []ctdf.TransportType) bool {
	railway := strings.ToLower(feature.Tags["railway"])
	for _, transportType := range transportTypes {
		switch transportType {
		case ctdf.TransportTypeRail:
			if railway == "rail" || strings.EqualFold(feature.Tags["train"], "yes") {
				return true
			}
		case ctdf.TransportTypeMetro:
			if railway == "subway" || strings.EqualFold(feature.Tags["subway"], "yes") {
				return true
			}
		case ctdf.TransportTypeTram:
			if railway == "tram" || strings.EqualFold(feature.Tags["tram"], "yes") {
				return true
			}
		}
	}
	return false
}

func preferPlatformLineTracks(features []ctdf.OSMStopFeature, platformIndexes []int, trackIndexes []int) []int {
	platformLines := map[string]struct{}{}
	for _, index := range platformIndexes {
		for line := range featureLineTokens(features[index], true) {
			platformLines[line] = struct{}{}
		}
	}
	if len(platformLines) == 0 {
		return trackIndexes
	}

	matchingTracks := make([]int, 0, len(trackIndexes))
	for _, index := range trackIndexes {
		for line := range featureLineTokens(features[index], false) {
			if _, matches := platformLines[line]; matches {
				matchingTracks = append(matchingTracks, index)
				break
			}
		}
	}
	if len(matchingTracks) > 0 {
		return matchingTracks
	}
	return trackIndexes
}

func featureLineTokens(feature ctdf.OSMStopFeature, includeNote bool) map[string]struct{} {
	values := []string{feature.Tags["line"], feature.Tags["route_ref"]}
	if includeNote {
		values = append(values, feature.Tags["note"])
		if tflPlatformID := feature.Tags["ref:GB:tfl_uid"]; tflPlatformID != "" {
			parts := strings.Split(tflPlatformID, "-")
			values = append(values, parts[len(parts)-1])
		}
	}
	if feature.Tags["line"] == "" {
		values = append(values, feature.PrimaryName, feature.Tags["name"])
	}

	tokens := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return r == ';' || r == ',' || r == '/' || r == '|'
		}) {
			part = strings.TrimSpace(part)
			part = strings.TrimSuffix(part, " lines")
			part = strings.TrimSuffix(part, " line")
			part = strings.Map(func(r rune) rune {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					return r
				}
				return -1
			}, part)
			if part != "" {
				tokens[part] = struct{}{}
			}
		}
	}
	return tokens
}

func platformHasDirectionHint(feature ctdf.OSMStopFeature, platform string) bool {
	values := []string{
		strings.ToLower(platform),
		strings.ToLower(feature.PrimaryName),
		strings.ToLower(feature.Tags["name"]),
		strings.ToLower(feature.Tags["direction"]),
		strings.ToLower(feature.Tags["ref:GB:tfl_uid"]),
	}
	for _, value := range values {
		if strings.Contains(value, "northbound") || strings.Contains(value, "southbound") ||
			strings.Contains(value, "eastbound") || strings.Contains(value, "westbound") ||
			strings.Contains(value, "-nb-") || strings.Contains(value, "-sb-") ||
			strings.Contains(value, "-eb-") || strings.Contains(value, "-wb-") {
			return true
		}
	}
	return false
}

func directionAlignedTrackMatch(matches []trackMatch, direction projectedPoint) (trackMatch, bool) {
	if len(matches) < 2 || vectorLength(direction) == 0 {
		return trackMatch{}, false
	}

	bestIndex := -1
	bestAlignment := -1.0
	secondAlignment := -1.0
	for index, match := range matches {
		trackVector := projectedPoint{x: match.segmentEnd.x - match.segmentStart.x, y: match.segmentEnd.y - match.segmentStart.y}
		if vectorLength(trackVector) == 0 {
			continue
		}
		alignment := dot(trackVector, direction) / (vectorLength(trackVector) * vectorLength(direction))
		if alignment > bestAlignment {
			secondAlignment = bestAlignment
			bestAlignment = alignment
			bestIndex = index
		} else if alignment > secondAlignment {
			secondAlignment = alignment
		}
	}

	// Raw OSM way direction is only decisive when one nearby track strongly
	// follows the journey and the alternatives run clearly the other way.
	if bestIndex < 0 || bestAlignment < 0.2 || secondAlignment > -0.2 {
		return trackMatch{}, false
	}
	return matches[bestIndex], true
}

func trackIndexForPlatformStopPosition(features []ctdf.OSMStopFeature, platform string, transportTypes []ctdf.TransportType, trackIndexes []int, referenceLatitude float64) (int, bool) {
	wanted := platformTokens(platform)
	bestTrackIndex := -1
	bestDistance := math.Inf(1)

	for _, feature := range features {
		if feature.Type != ctdf.OSMStopFeatureTypeStopPosition || feature.Location == nil ||
			!validLocation(*feature.Location) || !featureMatchesTransport(feature, transportTypes) ||
			!platformTokenSetsIntersect(wanted, []string{feature.Ref, feature.LocalRef, feature.Tags["ref"], feature.Tags["local_ref"]}) {
			continue
		}

		stopPosition := projectLocation(*feature.Location, referenceLatitude)
		for _, trackIndex := range trackIndexes {
			trackPoints := featureProjectedPoints(features[trackIndex], referenceLatitude)
			for segmentIndex := 0; segmentIndex < len(trackPoints)-1; segmentIndex++ {
				trackPoint := closestPointOnSegment(stopPosition, trackPoints[segmentIndex], trackPoints[segmentIndex+1])
				distance := pointDistance(stopPosition, trackPoint)
				if distance < bestDistance {
					bestDistance = distance
					bestTrackIndex = trackIndex
				}
			}
		}
	}

	// Stop positions should lie on the railway way. Allow a little OSM drawing
	// imprecision, but do not use a remote or incorrectly grouped node.
	return bestTrackIndex, bestTrackIndex >= 0 && bestDistance <= 5
}

func platformTokenSetsIntersect(wanted map[string]struct{}, values []string) bool {
	for _, value := range values {
		for token := range platformTokens(value) {
			if _, matches := wanted[token]; matches {
				return true
			}
		}
	}
	return false
}

func platformTokens(value string) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == ';' || r == ',' || r == '/' || r == '|' || r == '\\'
	}) {
		part = strings.TrimSpace(part)
		for _, prefix := range []string{"platform ", "platform-", "plat ", "plat-"} {
			part = strings.TrimPrefix(part, prefix)
		}
		if separator := strings.Index(part, " - "); separator >= 0 {
			part = part[:separator]
		}
		part = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, part)
		if part != "" {
			tokens[part] = struct{}{}
		}
	}
	return tokens
}

func journeyDirection(stop ctdf.Location, previous *ctdf.Location, next *ctdf.Location, referenceLatitude float64) (projectedPoint, bool) {
	stopPoint := projectLocation(stop, referenceLatitude)
	var direction projectedPoint
	switch {
	case previous != nil && next != nil:
		previousPoint := projectLocation(*previous, referenceLatitude)
		nextPoint := projectLocation(*next, referenceLatitude)
		direction = projectedPoint{x: nextPoint.x - previousPoint.x, y: nextPoint.y - previousPoint.y}
	case next != nil:
		nextPoint := projectLocation(*next, referenceLatitude)
		direction = projectedPoint{x: nextPoint.x - stopPoint.x, y: nextPoint.y - stopPoint.y}
	case previous != nil:
		previousPoint := projectLocation(*previous, referenceLatitude)
		direction = projectedPoint{x: stopPoint.x - previousPoint.x, y: stopPoint.y - previousPoint.y}
	default:
		return projectedPoint{}, false
	}
	return direction, vectorLength(direction) > 1
}

func featureProjectedPoints(feature ctdf.OSMStopFeature, referenceLatitude float64) []projectedPoint {
	locations := feature.Geometry
	if len(locations) == 0 && feature.Location != nil {
		locations = []ctdf.Location{*feature.Location}
	}
	points := make([]projectedPoint, 0, len(locations))
	for _, location := range locations {
		if validLocation(location) {
			points = append(points, projectLocation(location, referenceLatitude))
		}
	}
	return points
}

func platformProjectedPoints(feature ctdf.OSMStopFeature, referenceLatitude float64) []projectedPoint {
	points := featureProjectedPoints(feature, referenceLatitude)
	if feature.Type != ctdf.OSMStopFeatureTypePlatform || len(points) < 4 || pointDistance(points[0], points[len(points)-1]) > 0.1 {
		return points
	}

	if centroid, ok := polygonCentroid(points); ok {
		return []projectedPoint{centroid}
	}
	return points
}

func polygonCentroid(points []projectedPoint) (projectedPoint, bool) {
	origin := points[0]
	var twiceArea float64
	var xNumerator float64
	var yNumerator float64
	for index := 0; index < len(points)-1; index++ {
		current := projectedPoint{x: points[index].x - origin.x, y: points[index].y - origin.y}
		next := projectedPoint{x: points[index+1].x - origin.x, y: points[index+1].y - origin.y}
		cross := current.x*next.y - next.x*current.y
		twiceArea += cross
		xNumerator += (current.x + next.x) * cross
		yNumerator += (current.y + next.y) * cross
	}
	if math.Abs(twiceArea) < 0.01 {
		return projectedPoint{}, false
	}
	return projectedPoint{
		x: origin.x + xNumerator/(3*twiceArea),
		y: origin.y + yNumerator/(3*twiceArea),
	}, true
}

func closestTrackMatch(platformPoints []projectedPoint, trackPoints []projectedPoint) (trackMatch, bool) {
	if len(platformPoints) == 0 || len(trackPoints) < 2 {
		return trackMatch{}, false
	}
	best := trackMatch{
		distance:   math.Inf(1),
		trackStart: trackPoints[0],
		trackEnd:   trackPoints[len(trackPoints)-1],
	}
	for _, platformPoint := range platformPoints {
		for index := 0; index < len(trackPoints)-1; index++ {
			trackPoint := closestPointOnSegment(platformPoint, trackPoints[index], trackPoints[index+1])
			distance := pointDistance(platformPoint, trackPoint)
			if distance < best.distance {
				best.distance = distance
				best.trackPoint = trackPoint
				best.platformPoint = platformPoint
				best.segmentStart = trackPoints[index]
				best.segmentEnd = trackPoints[index+1]
			}
		}
	}
	return best, !math.IsInf(best.distance, 1)
}

func competingTrackMatches(matches []trackMatch, best trackMatch) []trackMatch {
	competing := []trackMatch{best}
	for _, match := range matches[1:] {
		if match.platformFeatureIndex != best.platformFeatureIndex || match.trackFeatureIndex == best.trackFeatureIndex {
			continue
		}
		if trackMatchesAreConnected(best, match) {
			continue
		}
		if match.distance-best.distance < 1.5 || match.distance < best.distance*1.2 {
			competing = append(competing, match)
		}
	}
	return competing
}

func trackMatchesAreConnected(a trackMatch, b trackMatch) bool {
	aEndpoints := []projectedPoint{a.trackStart, a.trackEnd}
	bEndpoints := []projectedPoint{b.trackStart, b.trackEnd}
	for _, aEndpoint := range aEndpoints {
		for _, bEndpoint := range bEndpoints {
			if pointDistance(aEndpoint, bEndpoint) <= 2 {
				return true
			}
		}
	}
	return false
}

func trackMatchesAgreeOnDoorSide(matches []trackMatch, direction projectedPoint) bool {
	var expectedSide trainDoorSide
	for _, match := range matches {
		side, ok := trackMatchDoorSide(match, direction)
		if !ok {
			return false
		}
		if expectedSide == "" {
			expectedSide = side
			continue
		}
		if side != expectedSide {
			return false
		}
	}
	return expectedSide != ""
}

func trackMatchDoorSide(match trackMatch, direction projectedPoint) (trainDoorSide, bool) {
	trackVector := projectedPoint{x: match.segmentEnd.x - match.segmentStart.x, y: match.segmentEnd.y - match.segmentStart.y}
	if dot(trackVector, direction) < 0 {
		trackVector.x = -trackVector.x
		trackVector.y = -trackVector.y
	}
	if vectorLength(trackVector) == 0 || vectorLength(direction) == 0 {
		return trainDoorSideUnknown, false
	}
	alignment := math.Abs(dot(trackVector, direction) / (vectorLength(trackVector) * vectorLength(direction)))
	if math.IsNaN(alignment) || alignment < 0.15 {
		return trainDoorSideUnknown, false
	}

	platformVector := projectedPoint{x: match.platformPoint.x - match.trackPoint.x, y: match.platformPoint.y - match.trackPoint.y}
	if vectorLength(platformVector) < 0.25 {
		return trainDoorSideUnknown, false
	}
	crossProduct := trackVector.x*platformVector.y - trackVector.y*platformVector.x
	if math.Abs(crossProduct) < 0.01 {
		return trainDoorSideUnknown, false
	}
	if crossProduct > 0 {
		return trainDoorSideLeft, true
	}
	return trainDoorSideRight, true
}

func closestPointOnSegment(point projectedPoint, start projectedPoint, end projectedPoint) projectedPoint {
	dx := end.x - start.x
	dy := end.y - start.y
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return start
	}
	t := ((point.x-start.x)*dx + (point.y-start.y)*dy) / lengthSquared
	t = math.Max(0, math.Min(1, t))
	return projectedPoint{x: start.x + t*dx, y: start.y + t*dy}
}

func projectLocation(location ctdf.Location, referenceLatitude float64) projectedPoint {
	return projectedPoint{
		x: location.Coordinates[0] * 111320 * math.Cos(referenceLatitude*math.Pi/180),
		y: location.Coordinates[1] * 110540,
	}
}

func validLocation(location ctdf.Location) bool {
	return len(location.Coordinates) >= 2 &&
		!math.IsNaN(location.Coordinates[0]) && !math.IsNaN(location.Coordinates[1]) &&
		!math.IsInf(location.Coordinates[0], 0) && !math.IsInf(location.Coordinates[1], 0)
}

func pointDistance(a projectedPoint, b projectedPoint) float64 {
	return math.Hypot(a.x-b.x, a.y-b.y)
}

func vectorLength(vector projectedPoint) float64 {
	return math.Hypot(vector.x, vector.y)
}

func dot(a projectedPoint, b projectedPoint) float64 {
	return a.x*b.x + a.y*b.y
}
