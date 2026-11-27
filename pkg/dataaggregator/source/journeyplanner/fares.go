package journeyplanner

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type fareCatalogue struct {
	datasetID                string
	linkedScheduleDatasetID  string
	versionFilter            bson.M
	rules                    []ctdf.FareLegRule
	transferRules            []ctdf.FareTransferRule
	effectiveJoins           []ctdf.FareEffectiveLegJoin
	caps                     []ctdf.FareCap
	locations                map[string]ctdf.FareLocation
	products                 map[string]ctdf.FareProduct
	riders                   map[string]ctdf.FareRiderCategory
	media                    map[string]ctdf.FareMedia
	offers                   map[string]ctdf.FareOffer
	timeframes               []ctdf.FareTimeframe
	routeNetworks            []ctdf.FareRouteNetwork
	serviceNetworks          map[string][]string
	explicitFareNetworks     map[string]bool
	explicitFareOrigins      map[string]bool
	explicitFareDestinations map[string]bool
	stopTimezones            map[string]string
	stopCRSCodes             map[string]map[string]bool
	reason                   string
}

func addFareQuotes(ctx context.Context, plans []ctdf.JourneyPlan, rider, media string) {
	catalogues := map[string]*fareCatalogue{}
	itemsByDataset := map[string][]ctdf.JourneyPlanRouteItem{}
	for index := range plans {
		for _, item := range planJourneyItems(plans[index]) {
			if item.Journey == nil || item.Journey.DataSource == nil {
				continue
			}
			datasetID := item.Journey.DataSource.DatasetID
			if datasetID != "" {
				itemsByDataset[datasetID] = append(itemsByDataset[datasetID], item)
			}
		}
	}
	for datasetID, items := range itemsByDataset {
		catalogues[datasetID] = loadFareCatalogue(ctx, datasetID, items, rider, media)
	}
	for index := range plans {
		plans[index].Fare = calculatePlanFare(ctx, plans[index], catalogues, rider, media)
	}
}

func planJourneyItems(plan ctdf.JourneyPlan) []ctdf.JourneyPlanRouteItem {
	journeyItems := make([]ctdf.JourneyPlanRouteItem, 0, 2)
	for _, item := range plan.RouteItems {
		if item.Journey != nil {
			journeyItems = append(journeyItems, item)
		}
	}
	return journeyItems
}

type effectiveFareSegment struct {
	item          ctdf.JourneyPlanRouteItem
	originalItems []ctdf.JourneyPlanRouteItem
}

func buildEffectiveFareSegments(ctx context.Context, items []ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue) ([]effectiveFareSegment, string) {
	if len(items) == 0 {
		return nil, "The itinerary has no fareable transit legs."
	}
	groups := [][]ctdf.JourneyPlanRouteItem{{items[0]}}
	for index := 1; index < len(items); index++ {
		joined, reason := fareLegsJoin(ctx, items[index-1], items[index], catalogues)
		if reason != "" {
			return nil, reason
		}
		if joined {
			groups[len(groups)-1] = append(groups[len(groups)-1], items[index])
		} else {
			groups = append(groups, []ctdf.JourneyPlanRouteItem{items[index]})
		}
	}

	segments := make([]effectiveFareSegment, 0, len(groups))
	for index, group := range groups {
		item, reason := makeEffectiveFareItem(ctx, group, index, catalogues)
		if reason != "" {
			return nil, reason
		}
		segments = append(segments, effectiveFareSegment{item: item, originalItems: group})
	}
	return segments, ""
}

func fareLegsJoin(ctx context.Context, from, to ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue) (bool, string) {
	if from.Journey == nil || to.Journey == nil || from.Journey.DataSource == nil || to.Journey.DataSource == nil {
		return false, ""
	}
	fromCatalogue := catalogues[from.Journey.DataSource.DatasetID]
	toCatalogue := catalogues[to.Journey.DataSource.DatasetID]
	if fromCatalogue == nil || toCatalogue == nil || fromCatalogue.datasetID != toCatalogue.datasetID ||
		fromCatalogue.versionFilter["fareversionid"] != toCatalogue.versionFilter["fareversionid"] {
		if fromCatalogue != nil && len(fromCatalogue.effectiveJoins) > 0 {
			return false, "A fare join rule spans separate fare catalogues and cannot be resolved safely."
		}
		return false, ""
	}
	catalogue := fromCatalogue
	if len(catalogue.effectiveJoins) == 0 {
		return false, ""
	}
	fromNetworks := catalogue.serviceNetworksFor(ctx, from.Journey.ServiceRef)
	toNetworks := catalogue.serviceNetworksFor(ctx, to.Journey.ServiceRef)
	_, fromCall, _, _, fromOK := fareItemEndpointCalls(from)
	toCall, _, _, _, toOK := fareItemEndpointCalls(to)
	if !fromOK || !toOK || fromCall == nil || toCall == nil {
		return false, "A fare join could not resolve the transfer stop occurrences."
	}
	unresolved := false
	for _, join := range catalogue.effectiveJoins {
		if join.FromNetworkRef != "" {
			if len(fromNetworks) == 0 || len(toNetworks) == 0 {
				unresolved = true
				continue
			}
			if !containsFareRef(fromNetworks, join.FromNetworkRef) || !containsFareRef(toNetworks, join.ToNetworkRef) {
				continue
			}
		}
		if join.FromStopRef != "" {
			if fromCall.StopRef != join.FromStopRef || toCall.StopRef != join.ToStopRef {
				continue
			}
		} else if fromCall.StopRef == "" || toCall.StopRef == "" {
			unresolved = true
			continue
		} else if fromCall.StopRef != toCall.StopRef {
			continue
		}
		_, fromArrival := fareItemValidationTimes(from)
		toDeparture, _ := fareItemValidationTimes(to)
		if join.FromTimeframeRef != "" {
			matches, known := fareTimeframeMatches(join.FromTimeframeRef, fromArrival, from.Journey, fromCall, catalogue.timeframes, catalogue.stopTimezones)
			if !known {
				unresolved = true
				continue
			}
			if !matches {
				continue
			}
		}
		if join.ToTimeframeRef != "" {
			matches, known := fareTimeframeMatches(join.ToTimeframeRef, toDeparture, to.Journey, toCall, catalogue.timeframes, catalogue.stopTimezones)
			if !known {
				unresolved = true
				continue
			}
			if !matches {
				continue
			}
		}
		return true, ""
	}
	if unresolved {
		return false, "A fare join rule depends on missing network, stop, or timeframe data."
	}
	return false, ""
}

func makeEffectiveFareItem(ctx context.Context, items []ctdf.JourneyPlanRouteItem, segmentIndex int, catalogues map[string]*fareCatalogue) (ctdf.JourneyPlanRouteItem, string) {
	if len(items) == 1 {
		return items[0], ""
	}
	first, last := items[0], items[len(items)-1]
	if first.Journey == nil || last.Journey == nil {
		return ctdf.JourneyPlanRouteItem{}, "An effective fare leg is missing its source journey."
	}
	origin, _, _, _, originOK := fareItemEndpointCalls(first)
	_, destination, _, _, destinationOK := fareItemEndpointCalls(last)
	if !originOK || !destinationOK || origin == nil || destination == nil {
		return ctdf.JourneyPlanRouteItem{}, "An effective fare leg could not resolve its endpoint stop occurrences."
	}
	calls, changeCallIndexes, reason := joinedFareJourneyCalls(items)
	if reason != "" {
		return ctdf.JourneyPlanRouteItem{}, reason
	}

	journey := *first.Journey
	if first.Journey.OtherIdentifiers != nil {
		journey.OtherIdentifiers = make(map[string]string, len(first.Journey.OtherIdentifiers))
		for key, value := range first.Journey.OtherIdentifiers {
			journey.OtherIdentifiers[key] = value
		}
	}
	journey.Calls = calls
	journey.PrimaryIdentifier = fmt.Sprintf("fare-effective-%d-%s-%s", segmentIndex, first.Journey.PrimaryIdentifier, last.Journey.PrimaryIdentifier)
	delete(journey.OtherIdentifiers, "FareChangeCallIndexes")
	if len(changeCallIndexes) > 0 {
		if journey.OtherIdentifiers == nil {
			journey.OtherIdentifiers = map[string]string{}
		}
		journey.OtherIdentifiers["FareChangeCallIndexes"] = joinFareIndexes(changeCallIndexes)
	}
	if journey.OtherIdentifiers == nil {
		journey.OtherIdentifiers = map[string]string{}
	}
	operatorRefs := make([]string, 0, len(items))
	for _, routeItem := range items {
		if routeItem.Journey != nil && routeItem.Journey.OperatorRef != "" && !containsFareRef(operatorRefs, routeItem.Journey.OperatorRef) {
			operatorRefs = append(operatorRefs, routeItem.Journey.OperatorRef)
		}
	}
	journey.OtherIdentifiers["FareOperatorRefs"] = strings.Join(operatorRefs, ",")
	journey.ServiceRef = first.Journey.ServiceRef
	journey.OperatorRef = first.Journey.OperatorRef
	journey.DepartureTime = first.StartTime
	journey.DepartureTimezone = first.Journey.DepartureTimezone
	if journey.DepartureTimezone == "" && origin.Stop != nil {
		journey.DepartureTimezone = origin.Stop.Timezone
	}
	servicesDiffer := false
	operatorsDiffer := false
	for _, routeItem := range items[1:] {
		if routeItem.Journey == nil {
			return ctdf.JourneyPlanRouteItem{}, "A joined fare leg is missing one of its source journeys."
		}
		servicesDiffer = servicesDiffer || routeItem.Journey.ServiceRef != first.Journey.ServiceRef
		operatorsDiffer = operatorsDiffer || routeItem.Journey.OperatorRef != first.Journey.OperatorRef
	}
	if servicesDiffer && effectiveFareHasRouteRules(items, catalogues) {
		return ctdf.JourneyPlanRouteItem{}, "A joined fare leg uses multiple routes, but the catalogue has route-specific rules."
	}
	if operatorsDiffer && effectiveFareHasOperatorRules(items, catalogues) {
		return ctdf.JourneyPlanRouteItem{}, "A joined fare leg uses multiple operators, but the catalogue has operator-specific rules."
	}

	if servicesDiffer {
		journey.ServiceRef = journey.PrimaryIdentifier
	}
	if operatorsDiffer {
		journey.OperatorRef = ""
	}
	firstCatalogue := catalogues[first.Journey.DataSource.DatasetID]
	if firstCatalogue != nil {
		if effectiveFareHasNetworkRules(items, catalogues) {
			for _, routeItem := range items {
				if len(firstCatalogue.serviceNetworksFor(ctx, routeItem.Journey.ServiceRef)) == 0 {
					return ctdf.JourneyPlanRouteItem{}, "A joined fare leg could not resolve the route networks required by fare rules."
				}
			}
		}
		commonNetworks := catalogueCommonNetworks(ctx, firstCatalogue, items)
		if journey.ServiceRef != first.Journey.ServiceRef {
			firstCatalogue.serviceNetworks[journey.ServiceRef] = commonNetworks
		}
	}

	item := first
	item.Journey = &journey
	item.JourneyOriginStopIndex = 0
	item.JourneyDestinationStopIndex = len(calls) - 1
	item.OriginStopRef = origin.StopRef
	item.DestinationStopRef = destination.StopRef
	item.StartTime = first.StartTime
	item.ArrivalTime = last.ArrivalTime
	item.ScheduledStartTime, _ = fareItemValidationTimes(first)
	_, item.ScheduledArrivalTime = fareItemValidationTimes(last)
	return item, ""
}

func joinedFareJourneyCalls(items []ctdf.JourneyPlanRouteItem) ([]ctdf.JourneyCall, []int, string) {
	calls := make([]ctdf.JourneyCall, 0)
	changeCallIndexes := make([]int, 0, len(items)-1)
	for itemIndex, item := range items {
		if item.Journey == nil {
			return nil, nil, "A joined fare leg is missing one of its source journeys."
		}
		start, end, ok := fareItemLegRange(item)
		if !ok {
			return nil, nil, "A joined fare leg could not resolve its covered path."
		}
		firstCallInItem := true
		for legIndex := start; legIndex < end; legIndex++ {
			origin, destination := item.Journey.LegEndpoints(legIndex)
			if origin == nil || destination == nil {
				return nil, nil, "A joined fare leg could not resolve one of its covered stop occurrences."
			}
			if len(calls) == 0 || calls[len(calls)-1].StopRef != origin.StopRef {
				current := *origin
				current.Start = false
				current.End = false
				calls = append(calls, current)
			} else {
				previous := &calls[len(calls)-1]
				previous.Departure = origin.Departure
				if previous.Departure.IsZero() {
					previous.Departure = origin.Arrival
				}
				previous.Outgoing = origin.Outgoing
				if previous.Stop == nil {
					previous.Stop = origin.Stop
				}
			}
			if itemIndex > 0 && firstCallInItem {
				changeCallIndexes = append(changeCallIndexes, len(calls)-1)
			}
			firstCallInItem = false
			current := *destination
			current.Start = false
			current.End = false
			calls = append(calls, current)
		}
	}
	if len(calls) < 2 {
		return nil, nil, "A joined fare leg does not contain at least two endpoint stop occurrences."
	}
	calls[0].Start = true
	calls[len(calls)-1].End = true
	calls[len(calls)-1].Outgoing = nil
	return calls, changeCallIndexes, ""
}

func joinFareIndexes(indexes []int) string {
	values := make([]string, len(indexes))
	for index, value := range indexes {
		values[index] = strconv.Itoa(value)
	}
	return strings.Join(values, ",")
}

func catalogueCommonNetworks(ctx context.Context, catalogue *fareCatalogue, items []ctdf.JourneyPlanRouteItem) []string {
	if catalogue == nil || len(items) == 0 {
		return nil
	}
	common := catalogue.serviceNetworksFor(ctx, items[0].Journey.ServiceRef)
	for _, item := range items[1:] {
		networks := catalogue.serviceNetworksFor(ctx, item.Journey.ServiceRef)
		filtered := make([]string, 0, len(common))
		for _, network := range common {
			if containsFareRef(networks, network) {
				filtered = append(filtered, network)
			}
		}
		common = filtered
	}
	return append([]string(nil), common...)
}

func effectiveFareHasRouteRules(items []ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue) bool {
	return effectiveFareHasRule(items, catalogues, func(rule ctdf.FareLegRule) bool { return rule.RouteRef != "" })
}

func effectiveFareHasOperatorRules(items []ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue) bool {
	return effectiveFareHasRule(items, catalogues, func(rule ctdf.FareLegRule) bool { return rule.OperatorRef != "" })
}

func effectiveFareHasNetworkRules(items []ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue) bool {
	return effectiveFareHasRule(items, catalogues, func(rule ctdf.FareLegRule) bool { return rule.NetworkRef != "" })
}

func effectiveFareHasRule(items []ctdf.JourneyPlanRouteItem, catalogues map[string]*fareCatalogue, predicate func(ctdf.FareLegRule) bool) bool {
	for _, item := range items {
		if item.Journey == nil || item.Journey.DataSource == nil {
			continue
		}
		if catalogue := catalogues[item.Journey.DataSource.DatasetID]; catalogue != nil {
			for _, rule := range catalogue.rules {
				if predicate(rule) {
					return true
				}
			}
		}
	}
	return false
}

func loadFareCatalogue(ctx context.Context, datasetID string, items []ctdf.JourneyPlanRouteItem, rider, media string) *fareCatalogue {
	catalogue := &fareCatalogue{
		linkedScheduleDatasetID:  datasetID,
		serviceNetworks:          map[string][]string{},
		explicitFareNetworks:     map[string]bool{},
		explicitFareOrigins:      map[string]bool{},
		explicitFareDestinations: map[string]bool{},
		stopCRSCodes:             map[string]map[string]bool{},
	}
	catalogue.routeNetworks = make([]ctdf.FareRouteNetwork, 0)
	head, err := activeFareVersionHead(ctx, datasetID)
	if err != nil {
		catalogue.reason = "The active fare catalogue could not be found."
		return catalogue
	}
	if head == nil {
		catalogue.reason = "No active fare catalogue is available for this schedule."
		return catalogue
	}
	catalogue.datasetID = head.DatasetID
	catalogue.versionFilter = bson.M{"datasetid": head.DatasetID, "fareversionid": head.ActiveVersionID}

	stopRefs := map[string]bool{}
	locationRefs := map[string]bool{}
	serviceRefs := map[string]bool{}
	stageNumbers := map[string]bool{}
	for _, item := range items {
		if item.Journey == nil {
			continue
		}
		if item.Journey.ServiceRef != "" {
			serviceRefs[item.Journey.ServiceRef] = true
		}
		start, end, ok := fareItemLegRange(item)
		if !ok {
			continue
		}
		for legIndex := start; legIndex < end; legIndex++ {
			origin, destination := item.Journey.LegEndpoints(legIndex)
			for _, call := range []*ctdf.JourneyCall{origin, destination} {
				if call == nil {
					continue
				}
				addFareRef(stopRefs, call.StopRef)
				addFareRef(locationRefs, call.StopRef)
			}
			if origin != nil && origin.Outgoing != nil {
				addFareRef(locationRefs, origin.Outgoing.OriginFareLocationRef)
				addFareRef(locationRefs, origin.Outgoing.DestinationFareLocationRef)
				addFareRef(stageNumbers, journeyFareStageNumber(origin.Outgoing.OriginFareLocationRef))
				addFareRef(stageNumbers, journeyFareStageNumber(origin.Outgoing.DestinationFareLocationRef))
			}
		}
	}
	catalogue.stopTimezones = readFareStopTimezones(ctx, sortedFareRefs(stopRefs))
	locationFilter := fareLocationLookupFilter(catalogue.versionFilter, sortedFareRefs(locationRefs), sortedFareRefs(stopRefs), sortedFareRefs(stageNumbers))
	catalogue.locations = readFareLocations(ctx, locationFilter)
	if catalogue.locations == nil {
		catalogue.reason = "The active fare locations could not be read."
		return catalogue
	}
	if !loadFareLocationParents(ctx, catalogue.versionFilter, catalogue.locations) {
		catalogue.reason = "The active fare location groups could not be read."
		return catalogue
	}

	lookupRefs := map[string]bool{}
	for ref := range locationRefs {
		lookupRefs[ref] = true
	}
	for ref := range stopRefs {
		lookupRefs[ref] = true
	}
	for ref := range catalogue.locations {
		lookupRefs[ref] = true
	}
	rulesFilter := fareFilterAnd(catalogue.versionFilter,
		fareRefFieldMatch("originref", append([]string{""}, sortedFareRefs(lookupRefs)...)),
		fareRefFieldMatch("destinationref", append([]string{""}, sortedFareRefs(lookupRefs)...)),
	)
	cursor, err := database.GetCollection("fare_leg_rules").Find(ctx, rulesFilter)
	if err != nil {
		catalogue.reason = "The active fare catalogue could not be read."
		return catalogue
	}
	if err := cursor.All(ctx, &catalogue.rules); err != nil {
		_ = cursor.Close(ctx)
		catalogue.reason = "The active fare catalogue could not be read."
		return catalogue
	}
	_ = cursor.Close(ctx)
	var emptySemanticsRules []ctdf.FareLegRule
	cursor, err = database.GetCollection("fare_leg_rules").Find(ctx,
		fareFilterAnd(catalogue.versionFilter, bson.M{"emptyfieldsarefallback": true}),
		options.Find().SetProjection(bson.M{"networkref": 1, "originref": 1, "destinationref": 1}),
	)
	if err != nil {
		catalogue.reason = "The active fare catalogue could not be read."
		return catalogue
	}
	if err := cursor.All(ctx, &emptySemanticsRules); err != nil {
		_ = cursor.Close(ctx)
		catalogue.reason = "The active fare catalogue could not be read."
		return catalogue
	}
	_ = cursor.Close(ctx)
	if len(catalogue.rules) == 0 {
		catalogue.reason = "The active fare catalogue has no evaluable leg rules."
		return catalogue
	}

	productRefs := map[string]bool{}
	offerRefs := map[string]bool{}
	riderRefs := map[string]bool{}
	mediaRefs := map[string]bool{}
	legGroupRefs := map[string]bool{}
	timeframeRefs := map[string]bool{}
	allRuleLocationRefs := map[string]bool{}
	for _, rule := range catalogue.rules {
		for _, ref := range rule.ProductRefs {
			addFareRef(productRefs, ref)
		}
		for _, ref := range rule.OfferRefs {
			addFareRef(offerRefs, ref)
		}
		addFareRef(productRefs, rule.ProductRef)
		addFareRef(offerRefs, rule.OfferRef)
		addFareRef(riderRefs, rule.RiderRef)
		addFareRef(mediaRefs, rule.MediaRef)
		addFareRef(legGroupRefs, rule.LegGroupRef)
		addFareRef(timeframeRefs, rule.FromTimeframeRef)
		addFareRef(timeframeRefs, rule.ToTimeframeRef)
		addFareRef(allRuleLocationRefs, rule.OriginRef)
		addFareRef(allRuleLocationRefs, rule.DestinationRef)
		for _, ref := range rule.ViaRefs {
			addFareRef(allRuleLocationRefs, ref)
		}
	}
	for _, rule := range emptySemanticsRules {
		addFareRef(catalogue.explicitFareNetworks, rule.NetworkRef)
		addFareRef(catalogue.explicitFareOrigins, rule.OriginRef)
		addFareRef(catalogue.explicitFareDestinations, rule.DestinationRef)
		addFareRef(allRuleLocationRefs, rule.OriginRef)
		addFareRef(allRuleLocationRefs, rule.DestinationRef)
	}
	for ref := range allRuleLocationRefs {
		if _, exists := catalogue.locations[ref]; !exists {
			catalogue.locations[ref] = ctdf.FareLocation{}
		}
	}
	missingLocationRefs := map[string]bool{}
	for ref := range allRuleLocationRefs {
		if _, exists := catalogue.locations[ref]; !exists || catalogue.locations[ref].PrimaryIdentifier == "" {
			missingLocationRefs[ref] = true
		}
	}
	if len(missingLocationRefs) > 0 {
		missing := readFareLocations(ctx, fareLocationLookupFilter(catalogue.versionFilter, sortedFareRefs(missingLocationRefs), nil, nil))
		if missing == nil {
			catalogue.reason = "The active fare location references could not be read."
			return catalogue
		}
		for ref, location := range missing {
			catalogue.locations[ref] = location
		}
		if !loadFareLocationParents(ctx, catalogue.versionFilter, catalogue.locations) {
			catalogue.reason = "The active fare location groups could not be read."
			return catalogue
		}
	}

	catalogue.products = readFareProducts(ctx, fareIdentifierFilter(catalogue.versionFilter, sortedFareRefs(productRefs)))
	if catalogue.products == nil {
		catalogue.reason = "The active fare products could not be read."
		return catalogue
	}
	for _, product := range catalogue.products {
		addFareRef(riderRefs, product.RiderRef)
		addFareRef(mediaRefs, product.MediaRef)
	}
	catalogue.offers = readFareOffers(ctx, fareIdentifierFilter(catalogue.versionFilter, sortedFareRefs(offerRefs)))
	catalogue.riders = readFareRiders(ctx, fareRiderFilter(catalogue.versionFilter, sortedFareRefs(riderRefs), rider))
	catalogue.media = readFareMedia(ctx, fareMediaFilter(catalogue.versionFilter, sortedFareRefs(mediaRefs), media))
	catalogue.caps = readFareCaps(ctx, catalogue.versionFilter)
	if catalogue.offers == nil || catalogue.riders == nil || catalogue.media == nil || catalogue.caps == nil {
		catalogue.reason = "The active fare product options could not be read."
		return catalogue
	}

	serviceIDs := sortedFareRefs(serviceRefs)
	if len(serviceIDs) > 0 {
		var services []ctdf.Service
		if err := readFareCollection(ctx, "services", bson.M{"primaryidentifier": bson.M{"$in": serviceIDs}}, &services); err != nil {
			catalogue.reason = "The active fare service networks could not be read."
			return catalogue
		}
		for _, service := range services {
			if service.NetworkRef != "" {
				catalogue.serviceNetworks[service.PrimaryIdentifier] = []string{service.NetworkRef}
			}
		}
		catalogue.routeNetworks = readFareRouteNetworks(ctx, fareFilterAnd(catalogue.versionFilter,
			bson.M{"routeref": bson.M{"$in": serviceIDs}},
		))
	}
	if catalogue.routeNetworks == nil {
		catalogue.reason = "The active fare route networks could not be read."
		return catalogue
	}
	networkRefs := map[string]bool{}
	for _, networks := range catalogue.serviceNetworks {
		for _, ref := range networks {
			addFareRef(networkRefs, ref)
		}
	}
	for _, routeNetwork := range catalogue.routeNetworks {
		addFareRef(networkRefs, routeNetwork.NetworkRef)
		if !containsFareRef(catalogue.serviceNetworks[routeNetwork.RouteRef], routeNetwork.NetworkRef) {
			catalogue.serviceNetworks[routeNetwork.RouteRef] = append(catalogue.serviceNetworks[routeNetwork.RouteRef], routeNetwork.NetworkRef)
		}
	}
	catalogue.effectiveJoins = readFareEffectiveJoins(ctx, fareEffectiveJoinFilter(catalogue.versionFilter, sortedFareRefs(stopRefs), sortedFareRefs(networkRefs)))
	for _, join := range catalogue.effectiveJoins {
		addFareRef(timeframeRefs, join.FromTimeframeRef)
		addFareRef(timeframeRefs, join.ToTimeframeRef)
	}
	catalogue.timeframes = readFareTimeframes(ctx, fareIdentifierFieldFilter(catalogue.versionFilter, "groupref", sortedFareRefs(timeframeRefs)))
	transferProductRefs := map[string]bool{}
	catalogue.transferRules = readFareTransferRules(ctx, fareTransferFilter(catalogue.versionFilter, sortedFareRefs(legGroupRefs)))
	if catalogue.transferRules == nil {
		catalogue.reason = "The active fare transfer rules could not be read."
		return catalogue
	}
	for _, rule := range catalogue.transferRules {
		addFareRef(transferProductRefs, rule.ProductRef)
		for _, ref := range rule.ProductRefs {
			addFareRef(transferProductRefs, ref)
		}
	}
	if len(transferProductRefs) > 0 {
		additionalProducts := readFareProducts(ctx, fareIdentifierFilter(catalogue.versionFilter, sortedFareRefs(transferProductRefs)))
		if additionalProducts == nil {
			catalogue.reason = "The active fare transfer products could not be read."
			return catalogue
		}
		for ref, product := range additionalProducts {
			catalogue.products[ref] = product
			addFareRef(riderRefs, product.RiderRef)
			addFareRef(mediaRefs, product.MediaRef)
		}
		additionalOffers := readFareOffers(ctx, fareFilterAnd(catalogue.versionFilter,
			bson.M{"productref": bson.M{"$in": sortedFareRefs(transferProductRefs)}},
		))
		if additionalOffers == nil {
			catalogue.reason = "The active fare transfer offers could not be read."
			return catalogue
		}
		for ref, offer := range additionalOffers {
			catalogue.offers[ref] = offer
		}
		additionalRiders := readFareRiders(ctx, fareRiderFilter(catalogue.versionFilter, sortedFareRefs(riderRefs), rider))
		additionalMedia := readFareMedia(ctx, fareMediaFilter(catalogue.versionFilter, sortedFareRefs(mediaRefs), media))
		if additionalRiders == nil || additionalMedia == nil {
			catalogue.reason = "The active fare transfer options could not be read."
			return catalogue
		}
		for ref, value := range additionalRiders {
			catalogue.riders[ref] = value
		}
		for ref, value := range additionalMedia {
			catalogue.media[ref] = value
		}
	}
	if catalogue.locations == nil || catalogue.transferRules == nil || catalogue.effectiveJoins == nil || catalogue.caps == nil || catalogue.products == nil || catalogue.riders == nil || catalogue.media == nil || catalogue.offers == nil || catalogue.timeframes == nil || catalogue.routeNetworks == nil {
		catalogue.reason = "The active fare catalogue is incomplete."
	}
	return catalogue
}

func calculatePlanFare(ctx context.Context, plan ctdf.JourneyPlan, catalogues map[string]*fareCatalogue, rider, media string) *ctdf.FareQuote {
	return calculatePlanFareWithMatches(ctx, plan, catalogues, rider, media, nil)
}

type fareMatchedProduct struct {
	alternative          ctdf.FareQuoteAlternative
	legGroupRef          string
	offerRef             string
	legacyTransferPolicy *legacyFareTransferPolicy
}

type legacyFareTransferPolicy struct {
	maxTransfers            *int
	transfersUnlimited      bool
	transferDurationSeconds *int64
}

func legacyFareTransferPolicyForProduct(product ctdf.FareProduct) *legacyFareTransferPolicy {
	if product.MaxTransfers == nil && !product.TransfersUnlimited && product.TransferDurationSeconds == nil {
		return nil
	}
	return &legacyFareTransferPolicy{
		maxTransfers:            product.MaxTransfers,
		transfersUnlimited:      product.TransfersUnlimited,
		transferDurationSeconds: product.TransferDurationSeconds,
	}
}

func calculatePlanFareWithMatches(ctx context.Context, plan ctdf.JourneyPlan, catalogues map[string]*fareCatalogue, rider, media string, matches *[]fareMatchedProduct) *ctdf.FareQuote {
	return calculatePlanFareWithScope(ctx, plan, catalogues, rider, media, matches, nil)
}

func calculatePlanFareWithScope(ctx context.Context, plan ctdf.JourneyPlan, catalogues map[string]*fareCatalogue, rider, media string, matches *[]fareMatchedProduct, itineraryScope []ctdf.JourneyPlanRouteItem) *ctdf.FareQuote {
	journeyItems := planJourneyItems(plan)
	if len(journeyItems) == 0 {
		return unavailableFare("This itinerary has no fareable transit leg.")
	}
	if len(journeyItems) > 1 {
		segments, reason := buildEffectiveFareSegments(ctx, journeyItems, catalogues)
		if reason != "" {
			return partialFare(reason)
		}
		quote := &ctdf.FareQuote{Certainty: ctdf.FareCertaintyComputed, Availability: ctdf.FareAvailabilityUnknown}
		perLegMatches := make([][]fareMatchedProduct, len(segments))
		segmentQuotes := make([]*ctdf.FareQuote, len(segments))
		allSameCatalogue := true
		firstCatalogueKey := ""
		for _, item := range journeyItems {
			if item.Journey == nil || item.Journey.DataSource == nil {
				continue
			}
			catalogue := catalogues[item.Journey.DataSource.DatasetID]
			if catalogue == nil {
				allSameCatalogue = false
				continue
			}
			catalogueKey := catalogue.datasetID + "\x00" + fmt.Sprint(catalogue.versionFilter["fareversionid"])
			if firstCatalogueKey == "" {
				firstCatalogueKey = catalogueKey
			} else if catalogueKey != firstCatalogueKey {
				allSameCatalogue = false
			}
		}
		for index, segment := range segments {
			item := segment.item
			itemPlan := ctdf.JourneyPlan{RouteItems: []ctdf.JourneyPlanRouteItem{item}, StartTime: item.StartTime, ArrivalTime: item.ArrivalTime}
			if itemPlan.StartTime.IsZero() {
				itemPlan.StartTime = plan.StartTime
			}
			scope := []ctdf.JourneyPlanRouteItem(nil)
			if allSameCatalogue {
				scope = journeyItems
			}
			componentQuote := calculatePlanFareWithScope(ctx, itemPlan, catalogues, rider, media, &perLegMatches[index], scope)
			segmentQuotes[index] = componentQuote
			for _, assumption := range componentQuote.PassengerAssumptions {
				if !containsFareRef(quote.PassengerAssumptions, assumption) {
					quote.PassengerAssumptions = append(quote.PassengerAssumptions, assumption)
				}
			}
			component := ctdf.FareQuoteComponent{Certainty: componentQuote.Certainty, Reason: componentQuote.Reason}
			if item.Journey != nil {
				if len(segment.originalItems) == 1 {
					component.JourneyRef = segment.originalItems[0].Journey.PrimaryIdentifier
				} else {
					for _, original := range segment.originalItems {
						if original.Journey != nil {
							component.JourneyRefs = append(component.JourneyRefs, original.Journey.PrimaryIdentifier)
						}
					}
				}
				if len(segment.originalItems) > 0 && segment.originalItems[0].Journey != nil {
					component.ServiceRef = segment.originalItems[0].Journey.ServiceRef
				}
				component.StartTime = item.StartTime
				component.ArrivalTime = item.ArrivalTime
				if origin, _, _, _, ok := fareItemEndpointCalls(item); ok && origin != nil {
					component.OriginStopRef = origin.StopRef
				}
				if _, destination, _, _, ok := fareItemEndpointCalls(item); ok && destination != nil {
					component.DestinationRef = destination.StopRef
				}
			}
			component.Price = componentQuote.Price
			component.Alternatives = componentQuote.Alternatives
			quote.Components = append(quote.Components, component)
		}
		if len(segments) == 1 {
			segmentQuotes[0].Components = quote.Components
			return segmentQuotes[0]
		}
		segmentItems := make([]ctdf.JourneyPlanRouteItem, len(segments))
		for index := range segments {
			segmentItems[index] = segments[index].item
		}
		combined, reason := combineFareLegs(segmentItems, perLegMatches, catalogues, allSameCatalogue, rider, media)
		if reason != "" {
			quote.Certainty = ctdf.FareCertaintyPartial
			quote.Reason = reason
			if matches != nil {
				*matches = nil
			}
			return quote
		}
		if len(combined) == 0 {
			quote.Certainty = ctdf.FareCertaintyPartial
			quote.Reason = "The fare catalogue does not contain enough leg and transfer detail to calculate a total."
			return quote
		}
		quote.Alternatives = combined
		quote.Price = &quote.Alternatives[0].Price
		for _, item := range journeyItems {
			if item.Journey != nil && item.Journey.DataSource != nil {
				quote.Source = item.Journey.DataSource.ProviderName
				if quote.Source == "" {
					quote.Source = item.Journey.DataSource.DatasetID
				}
				break
			}
		}
		for _, component := range quote.Components {
			if component.Certainty == ctdf.FareCertaintyPartial || component.Certainty == ctdf.FareCertaintyUnavailable {
				quote.Certainty = ctdf.FareCertaintyPartial
				quote.Reason = component.Reason
				if quote.Reason == "" {
					quote.Reason = "At least one transit leg has only a partial fare match."
				}
				break
			}
		}
		return quote
	}
	item := journeyItems[0]
	journey := item.Journey
	if journey.DataSource == nil || journey.DataSource.DatasetID == "" {
		return unavailableFare("The transit schedule has no linked fare dataset.")
	}
	catalogue := catalogues[journey.DataSource.DatasetID]
	if catalogue == nil || catalogue.linkedScheduleDatasetID != journey.DataSource.DatasetID {
		return unavailableFare("No active fare catalogue is available for this schedule.")
	}
	if catalogue.reason != "" {
		return unavailableFare(catalogue.reason)
	}
	if len(catalogue.caps) > 0 {
		return partialFare("The active fare catalogue contains fare caps, but prior fare spending is unavailable.")
	}
	riderRefs, riderAssumption, riderRequestFound := resolveFareRiderRefs(catalogue, rider)
	mediaRefs, mediaRequestFound := resolveFareMediaRefs(catalogue, media)
	if rider != "" && !riderRequestFound {
		return partialFare("The requested rider category is not present in the active fare catalogue.")
	}
	if media != "" && !mediaRequestFound {
		return partialFare("The requested fare media is not present in the active fare catalogue.")
	}
	requestedRailcards := selectedFareRailcards(catalogue, riderRefs)

	origin, destination, _, _, endpointsOK := fareItemEndpointCalls(item)
	if !endpointsOK || origin == nil || destination == nil {
		return partialFare("The fare catalogue could not resolve the boarded and alighted stop occurrences.")
	}

	serviceNetworks := catalogue.serviceNetworksFor(ctx, journey.ServiceRef)
	var service ctdf.Service
	serviceFound := database.GetCollection("services").FindOne(ctx, bson.M{"primaryidentifier": journey.ServiceRef}).Decode(&service) == nil

	quote := &ctdf.FareQuote{
		Certainty:    ctdf.FareCertaintyComputed,
		Availability: ctdf.FareAvailabilityUnknown,
		Source:       journey.DataSource.ProviderName,
	}
	if quote.Source == "" {
		quote.Source = journey.DataSource.DatasetID
	}
	if riderAssumption != "" {
		quote.PassengerAssumptions = []string{riderAssumption}
	}
	partialReason := ""
	deferredPartialReason := ""
	deferredPriority := -1
	dateFiltered := false
	passengerFiltered := false
	negativeFareProductMatched := false
	highestPriority := -1
	seenOffers := map[string]bool{}
	matchedProducts := make([]fareMatchedProduct, 0)
	for _, rule := range catalogue.rules {
		routeMatches := fareRouteMatches(rule.RouteRef, journey.ServiceRef, service, serviceFound)
		if !routeMatches {
			continue
		}
		if rule.OperatorRef != "" {
			operatorMatches := rule.OperatorRef == journey.OperatorRef
			if !operatorMatches {
				continue
			}
		}
		if rule.NetworkRef != "" && !containsFareRef(serviceNetworks, rule.NetworkRef) {
			continue
		}
		ruleOrigin, ruleDestination := origin, destination
		originFareLocationRef := fareItemLocationRef(item, true)
		destinationFareLocationRef := fareItemLocationRef(item, false)
		if rule.JourneyScope && len(itineraryScope) > 0 {
			first, last, valid := fareItineraryEndpoints(itineraryScope)
			if !valid {
				continue
			}
			ruleOrigin, _, _, _, _ = fareItemEndpointCalls(first)
			_, ruleDestination, _, _, _ = fareItemEndpointCalls(last)
			originFareLocationRef = fareItemLocationRef(first, true)
			destinationFareLocationRef = fareItemLocationRef(last, false)
		}
		if ruleOrigin == nil || ruleDestination == nil {
			continue
		}
		if rule.EmptyFieldsAreFallback {
			if rule.NetworkRef == "" && fareRefSetIntersects(serviceNetworks, catalogue.explicitFareNetworks) {
				continue
			}
			if rule.OriginRef == "" && fareLocationShouldSuppressFallback(catalogue.explicitFareOrigins, ruleOrigin.StopRef, originFareLocationRef, catalogue.locations) {
				continue
			}
			if rule.DestinationRef == "" && fareLocationShouldSuppressFallback(catalogue.explicitFareDestinations, ruleDestination.StopRef, destinationFareLocationRef, catalogue.locations) {
				continue
			}
		}
		if !fareLocationMatches(rule.OriginRef, ruleOrigin.StopRef, originFareLocationRef, catalogue.locations) ||
			!fareLocationMatches(rule.DestinationRef, ruleDestination.StopRef, destinationFareLocationRef, catalogue.locations) {
			continue
		}
		if len(rule.ViaRefs) > 0 {
			visits := false
			if rule.JourneyScope && len(itineraryScope) > 0 {
				visits = journeyItemsVisitFareLocations(itineraryScope, rule.ViaRefs, catalogue.locations)
			} else {
				visits = journeyItemVisitsFareLocations(item, rule.ViaRefs, catalogue.locations)
			}
			if !visits {
				continue
			}
		}
		rulePartialReason := ""
		if len(rule.UnresolvedConstraints) > 0 {
			rulePartialReason = appendFareReason(rulePartialReason, "A matching fare has route, restriction or validity conditions that are not yet evaluated: "+strings.Join(rule.UnresolvedConstraints, "; "))
		}
		if rule.RailConditions != nil {
			matches, reason := fareRailConditionsMatch(ctx, catalogue, rule.RailConditions, journey, item)
			if !matches {
				continue
			}
			if reason != "" {
				rulePartialReason = appendFareReason(rulePartialReason, reason)
			}
		}
		if rule.RiderRef != "" {
			if len(riderRefs) == 0 {
				if rule.RulePriority > deferredPriority {
					deferredPriority = rule.RulePriority
					deferredPartialReason = "A matching fare requires a rider category; the catalogue has no default adult category."
				} else if rule.RulePriority == deferredPriority {
					deferredPartialReason = appendFareReason(deferredPartialReason, "A matching fare requires a rider category; the catalogue has no default adult category.")
				}
				continue
			}
			if !riderRefs[rule.RiderRef] {
				if len(requestedRailcards) == 0 || !fareRiderIsAdult(rule.RiderRef, catalogue) {
					continue
				}
				rulePartialReason = appendFareReason(rulePartialReason, "The selected railcard is recognized, but its discount has not been calculated; the displayed price is the published adult fare.")
			}
		}
		if media != "" && rule.MediaRef != "" {
			if !mediaRefs[rule.MediaRef] {
				continue
			}
		}
		departureTime, arrivalTime := fareItemValidationTimes(item)
		fromTimeMatches, fromTimeKnown := fareTimeframeMatches(rule.FromTimeframeRef, departureTime, journey, origin, catalogue.timeframes, catalogue.stopTimezones)
		toTimeMatches, toTimeKnown := fareTimeframeMatches(rule.ToTimeframeRef, arrivalTime, journey, destination, catalogue.timeframes, catalogue.stopTimezones)
		if !fromTimeKnown || !toTimeKnown {
			if rule.RulePriority > deferredPriority {
				deferredPriority = rule.RulePriority
				deferredPartialReason = "A matching fare depends on a timeframe that could not be resolved for this service."
			} else if rule.RulePriority == deferredPriority {
				deferredPartialReason = appendFareReason(deferredPartialReason, "A matching fare depends on a timeframe that could not be resolved for this service.")
			}
			continue
		}
		if !fromTimeMatches || !toTimeMatches {
			continue
		}
		productRefs := rule.ProductRefs
		if len(productRefs) == 0 && rule.ProductRef != "" {
			productRefs = []string{rule.ProductRef}
		}
		offerRefs := rule.OfferRefs
		if len(offerRefs) == 0 && rule.OfferRef != "" {
			offerRefs = []string{rule.OfferRef}
		}
		productEligible := len(productRefs) == 0 || len(offerRefs) != len(productRefs)
		for variantIndex, productRef := range productRefs {
			if variantIndex >= len(offerRefs) {
				break
			}
			product, productExists := catalogue.products[productRef]
			offer, offerExists := catalogue.offers[offerRefs[variantIndex]]
			if !productExists || !offerExists || unsupportedFareProductType(product.Type) {
				productEligible = true
				break
			}
			if !fareOfferValidForProduct(offer, plan.StartTime, product, journey) || (product.RailTicket != nil && !product.RailTicket.LastValidDay.IsZero() && !fareTravelDateWithinLastValidDay(item.StartTime, product.RailTicket.LastValidDay, journey)) {
				dateFiltered = true
				continue
			}
			if product.RailTicket != nil && !fareRailTicketAllowsPassengerCount(*product.RailTicket, 1) {
				passengerFiltered = true
				continue
			}
			if product.RiderRef != "" {
				if len(riderRefs) == 0 {
					if rule.RulePriority > deferredPriority {
						deferredPriority = rule.RulePriority
						deferredPartialReason = "A matching fare requires a rider category; the catalogue has no default adult category."
					} else if rule.RulePriority == deferredPriority {
						deferredPartialReason = appendFareReason(deferredPartialReason, "A matching fare requires a rider category; the catalogue has no default adult category.")
					}
					continue
				}
				if !riderRefs[product.RiderRef] && (len(requestedRailcards) == 0 || !fareRiderIsAdult(product.RiderRef, catalogue)) {
					continue
				}
			}
			if media != "" && product.MediaRef != "" && !mediaRefs[product.MediaRef] {
				continue
			}
			productEligible = true
			break
		}
		if !productEligible {
			continue
		}
		if rule.RulePriority < highestPriority {
			continue
		}
		if rule.RulePriority > highestPriority {
			highestPriority = rule.RulePriority
			partialReason = ""
			quote.Alternatives = nil
			seenOffers = map[string]bool{}
			matchedProducts = nil
		}
		partialReason = appendFareReason(partialReason, rulePartialReason)
		if len(productRefs) == 0 || len(offerRefs) != len(productRefs) {
			partialReason = appendFareReason(partialReason, "A matching fare rule references a missing product or offer.")
			continue
		}
		for variantIndex, productRef := range productRefs {
			product, productExists := catalogue.products[productRef]
			offer, offerExists := catalogue.offers[offerRefs[variantIndex]]
			if !productExists || !offerExists {
				partialReason = appendFareReason(partialReason, "A matching fare rule references a missing product or offer.")
				continue
			}
			if unsupportedFareProductType(product.Type) {
				partialReason = appendFareReason(partialReason, "A matching fare product represents a pass or multi-trip entitlement that is not evaluated by this journey quote.")
				continue
			}
			if product.Type == ctdf.FareProductTypeReturn {
				partialReason = appendFareReason(partialReason, "A return ticket includes a return leg, but this quote only evaluates the outbound journey.")
			}
			if !fareOfferValidForProduct(offer, plan.StartTime, product, journey) {
				dateFiltered = true
				continue
			}
			if offer.Price.AmountMinor < 0 {
				// Negative GTFS Fares v2 products are transfer adjustments, not
				// standalone prices for a transit leg. Transfer rules resolve them
				// separately in combineFareLegs.
				negativeFareProductMatched = negativeFareProductMatched || matches == nil
				continue
			}
			if product.RailTicket != nil && !product.RailTicket.LastValidDay.IsZero() && !fareTravelDateWithinLastValidDay(item.StartTime, product.RailTicket.LastValidDay, journey) {
				dateFiltered = true
				continue
			}
			if product.RailTicket != nil && !fareRailTicketAllowsPassengerCount(*product.RailTicket, 1) {
				passengerFiltered = true
				continue
			}
			if product.RiderRef != "" {
				if len(riderRefs) == 0 {
					partialReason = appendFareReason(partialReason, "A matching fare requires a rider category; the catalogue has no default adult category.")
					continue
				}
				if !riderRefs[product.RiderRef] {
					if len(requestedRailcards) == 0 || !fareRiderIsAdult(product.RiderRef, catalogue) {
						continue
					}
					partialReason = appendFareReason(partialReason, "The selected railcard is recognized, but its discount has not been calculated; the displayed price is the published adult fare.")
				}
			}
			if media != "" && product.MediaRef != "" {
				if !mediaRefs[product.MediaRef] {
					continue
				}
			}
			mediaRef := rule.MediaRef
			if mediaRef == "" {
				mediaRef = product.MediaRef
			}
			alternativeKey := offer.PrimaryIdentifier + "\x00" + mediaRef
			conditions := append([]string(nil), offer.SalesConditions...)
			if product.RailTicket != nil {
				conditions = append(conditions, fareRailTicketConditions(*product.RailTicket)...)
			}
			if rule.RailConditions != nil {
				conditions = append(conditions, fareRailConditionDescriptions(rule.RailConditions)...)
			}
			for _, railcard := range requestedRailcards {
				conditions = append(conditions, "Railcard requested: "+railcard.Name+"; no railcard discount is included in this published adult fare.")
			}
			if offer.SalesOfferPackage != "" {
				conditions = append(conditions, "Purchase option: "+offer.SalesOfferPackage)
			}
			if product.PaymentMethod != "" {
				if paymentCondition := farePaymentCondition(product.PaymentMethod); paymentCondition != "" {
					conditions = append(conditions, paymentCondition)
				}
			}
			if mediaRef != "" {
				if fareMedia, exists := catalogue.media[mediaRef]; exists {
					conditions = append(conditions, "Ticket medium: "+fareMedia.Name)
				}
			}
			riderRef := rule.RiderRef
			if riderRef == "" {
				riderRef = product.RiderRef
			}
			if riderRef != "" {
				if rider, exists := catalogue.riders[riderRef]; exists {
					conditions = append(conditions, fareRiderCondition(rider))
				}
			}
			alternative := ctdf.FareQuoteAlternative{
				ProductRef:  product.PrimaryIdentifier,
				Name:        product.Name,
				Price:       offer.Price,
				Conditions:  conditions,
				PurchaseURL: offer.SalesURL,
			}
			matchedProducts = append(matchedProducts, fareMatchedProduct{
				alternative:          alternative,
				legGroupRef:          rule.LegGroupRef,
				offerRef:             offer.PrimaryIdentifier,
				legacyTransferPolicy: legacyFareTransferPolicyForProduct(product),
			})
			if !seenOffers[alternativeKey] {
				seenOffers[alternativeKey] = true
				quote.Alternatives = append(quote.Alternatives, alternative)
			}
		}
	}
	if deferredPriority >= highestPriority {
		partialReason = appendFareReason(partialReason, deferredPartialReason)
	}
	if len(quote.Alternatives) == 0 {
		if partialReason != "" {
			return partialFare(partialReason)
		}
		if negativeFareProductMatched {
			return partialFare("A matching negative fare product is a transfer discount and cannot price a single transit leg.")
		}
		if dateFiltered {
			return unavailableFare("No matching fare offers are valid on this journey date.")
		}
		if passengerFiltered {
			return unavailableFare("Matching rail tickets require a different passenger count; this quote assumes one passenger.")
		}
		return unavailableFare("No imported fare rules match this journey.")
	}
	if partialReason != "" {
		quote.Certainty = ctdf.FareCertaintyPartial
		quote.Reason = partialReason
		if matches != nil {
			*matches = matchedProducts
		}
		return quote
	}
	sort.Slice(quote.Alternatives, func(i, j int) bool {
		a, b := quote.Alternatives[i].Price, quote.Alternatives[j].Price
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		if a.Exponent != b.Exponent {
			return a.Exponent < b.Exponent
		}
		if a.AmountMinor != b.AmountMinor {
			return a.AmountMinor < b.AmountMinor
		}
		if quote.Alternatives[i].ProductRef != quote.Alternatives[j].ProductRef {
			return quote.Alternatives[i].ProductRef < quote.Alternatives[j].ProductRef
		}
		return strings.Join(quote.Alternatives[i].Conditions, "\x00") < strings.Join(quote.Alternatives[j].Conditions, "\x00")
	})
	first := quote.Alternatives[0].Price
	for _, alternative := range quote.Alternatives[1:] {
		if alternative.Price.Currency != first.Currency || alternative.Price.Exponent != first.Exponent {
			return partialFare("Matching fare products use different currencies and cannot be compared.")
		}
	}
	quote.Price = &first
	if matches != nil {
		*matches = matchedProducts
	}
	return quote
}

type fareAccumulation struct {
	price                ctdf.FareMoney
	productRefs          []string
	names                []string
	conditions           []string
	purchaseURL          string
	lastGroup            string
	transferRunFromGroup string
	transferRunToGroup   string
	transferRunCount     int
	transferRunStartItem int
	legacyProductRef     string
	legacyOfferRef       string
	legacyTransferPolicy *legacyFareTransferPolicy
	legacyStartItem      int
}

type fareTransferProduct struct {
	ref         string
	name        string
	price       ctdf.FareMoney
	conditions  []string
	purchaseURL string
}

func fareAccumulationKey(value fareAccumulation) string {
	policy := ""
	if value.legacyTransferPolicy != nil {
		maxTransfers := ""
		if value.legacyTransferPolicy.maxTransfers != nil {
			maxTransfers = strconv.Itoa(*value.legacyTransferPolicy.maxTransfers)
		}
		transferDuration := ""
		if value.legacyTransferPolicy.transferDurationSeconds != nil {
			transferDuration = strconv.FormatInt(*value.legacyTransferPolicy.transferDurationSeconds, 10)
		}
		policy = fmt.Sprintf("%s|%t|%s", maxTransfers, value.legacyTransferPolicy.transfersUnlimited, transferDuration)
	}
	return fmt.Sprintf("%d|%s|%d|%q|%q|%q|%q|%q|%q|%q|%d|%d|%d|%q|%q|%s",
		value.price.AmountMinor, value.price.Currency, value.price.Exponent,
		value.productRefs, value.names, value.conditions, value.purchaseURL, value.lastGroup,
		value.transferRunFromGroup, value.transferRunToGroup, value.transferRunCount,
		value.transferRunStartItem, value.legacyStartItem, value.legacyProductRef,
		value.legacyOfferRef, policy)
}

func appendFareAccumulation(values *[]fareAccumulation, seen map[string]bool, value fareAccumulation, limit int) bool {
	key := fareAccumulationKey(value)
	if seen[key] {
		return true
	}
	if len(*values) >= limit {
		return false
	}
	seen[key] = true
	*values = append(*values, cloneFareAccumulation(value))
	return true
}

func combineFareLegs(items []ctdf.JourneyPlanRouteItem, legMatches [][]fareMatchedProduct, catalogues map[string]*fareCatalogue, allSameCatalogue bool, rider, media string) ([]ctdf.FareQuoteAlternative, string) {
	if len(items) != len(legMatches) || len(items) < 2 {
		return nil, "The itinerary's fare legs could not be aligned with their fare rules."
	}
	legCatalogues := make([]*fareCatalogue, len(items))
	for index, item := range items {
		if item.Journey == nil || item.Journey.DataSource == nil {
			return nil, "A transit leg is missing its linked fare catalogue."
		}
		legCatalogues[index] = catalogues[item.Journey.DataSource.DatasetID]
		if legCatalogues[index] == nil || legCatalogues[index].reason != "" {
			return nil, "A transit leg's active fare catalogue could not be read."
		}
		if len(legCatalogues[index].caps) > 0 {
			return nil, "The active fare catalogue contains fare caps, but journey spending history is unavailable."
		}
		if len(legMatches[index]) == 0 {
			return nil, "At least one transit leg has no complete matching fare product."
		}
		for _, match := range legMatches[index] {
			product := legCatalogues[index].products[match.alternative.ProductRef]
			if unsupportedFareProductType(product.Type) {
				return nil, "A matching fare product represents a pass or multi-trip entitlement that is not evaluated by this journey quote."
			}
		}
	}
	if !allSameCatalogue {
		for _, catalogue := range legCatalogues {
			if len(catalogue.transferRules) > 0 {
				return nil, "Transfer rules span more than one fare catalogue and cannot be resolved together."
			}
		}
	}

	const maxFareCombinations = 256
	accumulations := make([]fareAccumulation, 0, min(len(legMatches[0]), maxFareCombinations))
	initialSeen := map[string]bool{}
	for _, match := range legMatches[0] {
		accumulation := fareAccumulation{
			price:       match.alternative.Price,
			productRefs: []string{match.alternative.ProductRef},
			names:       []string{match.alternative.Name},
			conditions:  append([]string(nil), match.alternative.Conditions...),
			purchaseURL: match.alternative.PurchaseURL,
			lastGroup:   match.legGroupRef,
		}
		setLegacyFareTransferState(&accumulation, match, 0)
		if !appendFareAccumulation(&accumulations, initialSeen, accumulation, maxFareCombinations) {
			return nil, "This itinerary produces too many fare combinations to compare safely."
		}
	}
	for legIndex := 1; legIndex < len(legMatches); legIndex++ {
		catalogue := legCatalogues[legIndex-1]
		if !allSameCatalogue {
			catalogue = nil
		}
		var next []fareAccumulation
		nextSeen := map[string]bool{}
		for _, accumulation := range accumulations {
			for _, match := range legMatches[legIndex] {
				transferCount := 1
				transferStartItem := legIndex - 1
				if accumulation.transferRunFromGroup == accumulation.lastGroup && accumulation.transferRunToGroup == match.legGroupRef && accumulation.transferRunCount > 0 {
					transferCount = accumulation.transferRunCount + 1
					transferStartItem = accumulation.transferRunStartItem
				}
				rules := matchingFareTransferRules(catalogue, accumulation.lastGroup, match.legGroupRef, transferCount)
				applicableRules := make([]ctdf.FareTransferRule, 0, len(rules))
				unresolvedDuration := false
				bestTransferCount := int(^uint(0) >> 1)
				for _, rule := range rules {
					applies, known := fareTransferDurationMatches(rule, items[transferStartItem], items[legIndex])
					if !known {
						unresolvedDuration = true
						continue
					}
					if !applies {
						continue
					}
					count := int(^uint(0) >> 1)
					if rule.FromLegGroupRef != rule.ToLegGroupRef {
						count = 1
					} else if rule.TransferCount != nil && *rule.TransferCount > 0 {
						count = *rule.TransferCount
					}
					if count < bestTransferCount {
						bestTransferCount = count
						applicableRules = nil
					}
					if count == bestTransferCount {
						applicableRules = append(applicableRules, rule)
					}
				}
				if unresolvedDuration {
					return nil, "A matching transfer rule depends on journey times that could not be resolved."
				}

				if len(applicableRules) == 0 {
					canContinue, known := canContinueLegacyFare(accumulation, match, items, legIndex)
					if !known {
						return nil, "A legacy GTFS transfer entitlement depends on journey times that could not be resolved."
					}
					if canContinue {
						value := cloneFareAccumulation(accumulation)
						value.lastGroup = match.legGroupRef
						value.transferRunFromGroup = ""
						value.transferRunToGroup = ""
						value.transferRunCount = 0
						value.transferRunStartItem = 0
						if !appendFareAccumulation(&next, nextSeen, value, maxFareCombinations) {
							return nil, "This itinerary produces too many fare combinations to compare safely."
						}
						continue
					}
					combined, ok := addFareMoney(accumulation.price, match.alternative.Price)
					if !ok {
						return nil, "Matching fares use different currencies or exceed the supported amount range."
					}
					value := cloneFareAccumulation(accumulation)
					value.price = combined
					value.productRefs = append(value.productRefs, match.alternative.ProductRef)
					value.names = append(value.names, match.alternative.Name)
					value.conditions = append(value.conditions, match.alternative.Conditions...)
					if value.purchaseURL != match.alternative.PurchaseURL {
						value.purchaseURL = ""
					}
					value.lastGroup = match.legGroupRef
					value.transferRunFromGroup = ""
					value.transferRunToGroup = ""
					value.transferRunCount = 0
					value.transferRunStartItem = 0
					setLegacyFareTransferState(&value, match, legIndex)
					if !appendFareAccumulation(&next, nextSeen, value, maxFareCombinations) {
						return nil, "This itinerary produces too many fare combinations to compare safely."
					}
					continue
				}

				for _, rule := range applicableRules {
					transferTime, _ := fareItemValidationTimes(items[legIndex])
					transferProducts, reason := fareTransferProducts(catalogue, rule, rider, media, transferTime)
					if reason != "" {
						return nil, reason
					}
					for _, transfer := range transferProducts {
						value := cloneFareAccumulation(accumulation)
						value.lastGroup = match.legGroupRef
						value.transferRunFromGroup = accumulation.lastGroup
						value.transferRunToGroup = match.legGroupRef
						value.transferRunCount = transferCount
						value.transferRunStartItem = transferStartItem
						switch rule.TransferType {
						case "0":
							price, ok := addFareMoney(value.price, transfer.price)
							if !ok {
								return nil, "A fare transfer uses a different currency or exceeds the supported amount range."
							}
							value.price = price
						case "1":
							price, ok := addFareMoney(value.price, transfer.price)
							if !ok {
								return nil, "A fare transfer uses a different currency or exceeds the supported amount range."
							}
							price, ok = addFareMoney(price, match.alternative.Price)
							if !ok {
								return nil, "Matching fares use different currencies or exceed the supported amount range."
							}
							value.price = price
							value.productRefs = append(value.productRefs, match.alternative.ProductRef)
							value.names = append(value.names, match.alternative.Name)
							value.conditions = append(value.conditions, match.alternative.Conditions...)
						case "2":
							transferPrice := transfer.price
							if transferPrice.Currency == "" {
								transferPrice.Currency = accumulation.price.Currency
								transferPrice.Exponent = accumulation.price.Exponent
							}
							if legIndex == 1 {
								// The first AB transfer replaces the A fare. Later AB
								// transfers are added to the already processed subtotal S.
								value.price = transferPrice
								value.productRefs = nil
								value.names = nil
								value.conditions = nil
								value.purchaseURL = transfer.purchaseURL
							} else {
								price, ok := addFareMoney(accumulation.price, transferPrice)
								if !ok {
									return nil, "A fare transfer uses a different currency or exceeds the supported amount range."
								}
								value.price = price
							}
						default:
							return nil, "The active fare catalogue contains an unsupported transfer type."
						}
						if transfer.ref != "" {
							value.productRefs = append(value.productRefs, transfer.ref)
						}
						if transfer.name != "" {
							value.names = append(value.names, transfer.name)
						}
						value.conditions = append(value.conditions, transfer.conditions...)
						clearLegacyFareTransferState(&value)
						if value.purchaseURL != transfer.purchaseURL {
							value.purchaseURL = ""
						}
						if !appendFareAccumulation(&next, nextSeen, value, maxFareCombinations) {
							return nil, "This itinerary produces too many fare combinations to compare safely."
						}
					}
				}
			}
		}
		accumulations = next
		if len(accumulations) == 0 {
			return nil, "No fare transfer option can be resolved for this itinerary."
		}
	}

	validAccumulations := accumulations[:0]
	for _, accumulation := range accumulations {
		if accumulation.price.AmountMinor < 0 {
			continue
		}
		validAccumulations = append(validAccumulations, accumulation)
	}
	accumulations = validAccumulations
	if len(accumulations) == 0 {
		return nil, "Transfer discounts would produce a negative total fare for this itinerary."
	}
	for index := range accumulations {
		if len(accumulations[index].productRefs) > 1 {
			accumulations[index].purchaseURL = ""
		}
	}
	result := make([]ctdf.FareQuoteAlternative, 0, len(accumulations))
	for _, accumulation := range accumulations {
		name := strings.Join(accumulation.names, " + ")
		if name == "" {
			name = "Fare for all transit legs"
		}
		productRef := ""
		if len(accumulation.productRefs) == 1 {
			productRef = accumulation.productRefs[0]
		}
		result = append(result, ctdf.FareQuoteAlternative{
			ProductRef:  productRef,
			ProductRefs: append([]string(nil), accumulation.productRefs...),
			Name:        name,
			Price:       accumulation.price,
			Conditions:  accumulation.conditions,
			PurchaseURL: accumulation.purchaseURL,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i].Price, result[j].Price
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		if a.Exponent != b.Exponent {
			return a.Exponent < b.Exponent
		}
		if a.AmountMinor != b.AmountMinor {
			return a.AmountMinor < b.AmountMinor
		}
		return strings.Join(result[i].ProductRefs, "\x00") < strings.Join(result[j].ProductRefs, "\x00")
	})
	first := result[0].Price
	for _, alternative := range result[1:] {
		if alternative.Price.Currency != first.Currency || alternative.Price.Exponent != first.Exponent {
			return nil, "Matching fares use different currencies and cannot be compared."
		}
	}
	return result, ""
}

func matchingFareTransferRules(catalogue *fareCatalogue, fromGroup, toGroup string, currentTransferCount int) []ctdf.FareTransferRule {
	if catalogue == nil {
		return nil
	}
	fromExact, toExact := false, false
	for _, rule := range catalogue.transferRules {
		fromExact = fromExact || (fromGroup != "" && rule.FromLegGroupRef == fromGroup)
		toExact = toExact || (toGroup != "" && rule.ToLegGroupRef == toGroup)
	}
	result := make([]ctdf.FareTransferRule, 0)
	for _, rule := range catalogue.transferRules {
		fromMatches := rule.FromLegGroupRef == fromGroup || (!fromExact && rule.FromLegGroupRef == "")
		toMatches := rule.ToLegGroupRef == toGroup || (!toExact && rule.ToLegGroupRef == "")
		if !fromMatches || !toMatches {
			continue
		}
		count := 1
		if rule.FromLegGroupRef == rule.ToLegGroupRef {
			if rule.TransferCount == nil {
				continue
			}
			count = *rule.TransferCount
			if count == -1 {
				count = int(^uint(0) >> 1)
			} else if count < currentTransferCount {
				continue
			}
		}
		result = append(result, rule)
	}
	return result
}

func fareTransferDurationMatches(rule ctdf.FareTransferRule, from, to ctdf.JourneyPlanRouteItem) (bool, bool) {
	if rule.DurationSeconds == nil {
		return true, true
	}
	fromDeparture, fromArrival := fareItemValidationTimes(from)
	toDeparture, toArrival := fareItemValidationTimes(to)
	var start, end time.Time
	switch rule.DurationType {
	case "0":
		start, end = fromDeparture, toArrival
	case "1":
		start, end = fromDeparture, toDeparture
	case "2":
		start, end = fromArrival, toDeparture
	case "3":
		start, end = fromArrival, toArrival
	default:
		return false, false
	}
	if start.IsZero() || end.IsZero() {
		return false, false
	}
	seconds := end.Sub(start).Seconds()
	return seconds >= 0 && seconds <= float64(*rule.DurationSeconds), true
}

func fareItemValidationTimes(item ctdf.JourneyPlanRouteItem) (time.Time, time.Time) {
	departure, arrival := item.ScheduledStartTime, item.ScheduledArrivalTime
	if departure.IsZero() {
		departure = item.StartTime
	}
	if arrival.IsZero() {
		arrival = item.ArrivalTime
	}
	if item.Journey != nil {
		origin, destination, _, _, endpointsOK := fareItemEndpointCalls(item)
		if departure.IsZero() {
			if endpointsOK && origin != nil {
				departure = origin.Departure
				if departure.IsZero() {
					departure = origin.Arrival
				}
			}
		}
		if arrival.IsZero() {
			if endpointsOK && destination != nil {
				arrival = destination.Arrival
				if arrival.IsZero() {
					arrival = destination.Departure
				}
			}
		}
	}
	return departure, arrival
}

func fareItemEndpointCalls(item ctdf.JourneyPlanRouteItem) (origin, destination *ctdf.JourneyCall, startLeg, endLeg int, ok bool) {
	if item.Journey == nil {
		return nil, nil, 0, 0, false
	}
	startLeg, endLeg = item.JourneyOriginStopIndex, item.JourneyDestinationStopIndex
	if startLeg >= 0 && endLeg > startLeg {
		origin, _ = item.Journey.LegEndpoints(startLeg)
		_, destination = item.Journey.LegEndpoints(endLeg - 1)
		if origin != nil && destination != nil && origin.StopRef == item.OriginStopRef && destination.StopRef == item.DestinationStopRef {
			return origin, destination, startLeg, endLeg, true
		}
	}
	// Older or externally constructed items may not carry path indexes. Resolve
	// by endpoint refs as a fallback, while preferring planner-provided indexes
	// to distinguish repeated visits to the same stop.
	startLeg, endLeg = graphLegStopIndexes(item.Journey, &item)
	if startLeg < 0 || endLeg <= startLeg {
		return nil, nil, 0, 0, false
	}
	origin, _ = item.Journey.LegEndpoints(startLeg)
	_, destination = item.Journey.LegEndpoints(endLeg - 1)
	if origin == nil || destination == nil || origin.StopRef != item.OriginStopRef || destination.StopRef != item.DestinationStopRef {
		return nil, nil, 0, 0, false
	}
	return origin, destination, startLeg, endLeg, true
}

func fareItemLegRange(item ctdf.JourneyPlanRouteItem) (int, int, bool) {
	_, _, startLeg, endLeg, ok := fareItemEndpointCalls(item)
	return startLeg, endLeg, ok
}

func fareTransferProducts(catalogue *fareCatalogue, rule ctdf.FareTransferRule, rider, media string, travelTime time.Time) ([]fareTransferProduct, string) {
	productRefs := rule.ProductRefs
	if len(productRefs) == 0 && rule.ProductRef != "" {
		productRefs = []string{rule.ProductRef}
	}
	if len(productRefs) == 0 {
		return []fareTransferProduct{{price: ctdf.FareMoney{Currency: ""}, name: "Free transfer"}}, ""
	}
	riderRefs, _, riderFound := resolveFareRiderRefs(catalogue, rider)
	mediaRefs, mediaFound := resolveFareMediaRefs(catalogue, media)
	if rider != "" && !riderFound {
		return nil, "The requested rider category is not present for a matching transfer fare."
	}
	if media != "" && !mediaFound {
		return nil, "The requested fare medium is not present for a matching transfer fare."
	}
	result := make([]fareTransferProduct, 0)
	for _, productRef := range productRefs {
		product, exists := catalogue.products[productRef]
		if !exists {
			return nil, "A matching transfer rule references a missing fare product."
		}
		if product.RiderRef != "" && (len(riderRefs) == 0 || !riderRefs[product.RiderRef]) {
			continue
		}
		if media != "" && product.MediaRef != "" && !mediaRefs[product.MediaRef] {
			continue
		}
		for _, offer := range catalogue.offers {
			if offer.ProductRef != productRef || !fareOfferValidForProduct(offer, travelTime, product, nil) {
				continue
			}
			conditions := append([]string(nil), offer.SalesConditions...)
			if product.MediaRef != "" {
				if fareMedia, exists := catalogue.media[product.MediaRef]; exists {
					conditions = append(conditions, "Ticket medium: "+fareMedia.Name)
				}
			}
			result = append(result, fareTransferProduct{ref: productRef, name: product.Name, price: offer.Price, conditions: conditions, purchaseURL: offer.SalesURL})
		}
	}
	if len(result) == 0 {
		return nil, "A matching transfer product has no eligible dated offer."
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ref != result[j].ref {
			return result[i].ref < result[j].ref
		}
		if result[i].price.Currency != result[j].price.Currency {
			return result[i].price.Currency < result[j].price.Currency
		}
		return result[i].price.AmountMinor < result[j].price.AmountMinor
	})
	return result, ""
}

func cloneFareAccumulation(value fareAccumulation) fareAccumulation {
	value.productRefs = append([]string(nil), value.productRefs...)
	value.names = append([]string(nil), value.names...)
	value.conditions = append([]string(nil), value.conditions...)
	return value
}

func setLegacyFareTransferState(value *fareAccumulation, match fareMatchedProduct, startItem int) {
	clearLegacyFareTransferState(value)
	if match.legacyTransferPolicy == nil {
		return
	}
	value.legacyProductRef = match.alternative.ProductRef
	value.legacyOfferRef = match.offerRef
	value.legacyTransferPolicy = match.legacyTransferPolicy
	value.legacyStartItem = startItem
}

func clearLegacyFareTransferState(value *fareAccumulation) {
	value.legacyProductRef = ""
	value.legacyOfferRef = ""
	value.legacyTransferPolicy = nil
	value.legacyStartItem = 0
}

func canContinueLegacyFare(accumulation fareAccumulation, match fareMatchedProduct, items []ctdf.JourneyPlanRouteItem, currentItem int) (bool, bool) {
	policy := accumulation.legacyTransferPolicy
	if policy == nil || match.legacyTransferPolicy == nil ||
		accumulation.legacyProductRef != match.alternative.ProductRef || accumulation.legacyOfferRef != match.offerRef {
		return false, true
	}
	transferCount := currentItem - accumulation.legacyStartItem
	if transferCount <= 0 {
		return false, true
	}
	if !policy.transfersUnlimited && (policy.maxTransfers == nil || transferCount > *policy.maxTransfers) {
		return false, true
	}
	if policy.transferDurationSeconds != nil {
		if accumulation.legacyStartItem < 0 || accumulation.legacyStartItem >= len(items) || currentItem < 0 || currentItem >= len(items) {
			return false, false
		}
		firstDeparture, _ := fareItemValidationTimes(items[accumulation.legacyStartItem])
		transferDeparture, _ := fareItemValidationTimes(items[currentItem])
		if firstDeparture.IsZero() || transferDeparture.IsZero() {
			return false, false
		}
		elapsed := transferDeparture.Sub(firstDeparture).Seconds()
		return elapsed >= 0 && elapsed <= float64(*policy.transferDurationSeconds), true
	}
	return true, true
}

func addFareMoney(left, right ctdf.FareMoney) (ctdf.FareMoney, bool) {
	if left.Currency == "" && left.AmountMinor == 0 {
		return right, true
	}
	if right.Currency == "" && right.AmountMinor == 0 {
		return left, true
	}
	maxInt64 := int64(^uint64(0) >> 1)
	minInt64 := -maxInt64 - 1
	if left.Currency != right.Currency || left.Exponent != right.Exponent ||
		(right.AmountMinor > 0 && left.AmountMinor > maxInt64-right.AmountMinor) ||
		(right.AmountMinor < 0 && left.AmountMinor < minInt64-right.AmountMinor) {
		return ctdf.FareMoney{}, false
	}
	left.AmountMinor += right.AmountMinor
	return left, true
}

func farePaymentCondition(method string) string {
	switch strings.TrimSpace(method) {
	case "0":
		return "Payment at boarding"
	case "1":
		return "Payment before boarding"
	default:
		return "Payment method: " + strings.TrimSpace(method)
	}
}

func unsupportedFareProductType(productType ctdf.FareProductType) bool {
	switch productType {
	case ctdf.FareProductTypeSingleRide, ctdf.FareProductTypeReturn:
		return false
	default:
		return true
	}
}

func fareTravelDateWithinLastValidDay(travelTime, lastValidDay time.Time, journey *ctdf.Journey) bool {
	if travelTime.IsZero() || lastValidDay.IsZero() {
		return true
	}
	travelDate := fareRailCalendarDate(travelTime, journey)
	lastDate := time.Date(lastValidDay.Year(), lastValidDay.Month(), lastValidDay.Day(), 0, 0, 0, 0, time.UTC)
	return !travelDate.After(lastDate)
}

func fareRailConditionDescriptions(conditions *ctdf.FareRailConditions) []string {
	values := make([]string, 0, len(conditions.Restrictions)+len(conditions.AdvancePurchase)+len(conditions.TOCSpecificTickets)+1)
	if conditions.Route != nil && conditions.Route.Description != "" {
		values = append(values, "Rail route: "+conditions.Route.Description)
	}
	for _, restriction := range conditions.Restrictions {
		if restriction.Description != "" {
			values = append(values, "Rail restriction: "+restriction.Description)
		} else if restriction.OutwardDescription != "" {
			values = append(values, "Rail restriction: "+restriction.OutwardDescription)
		}
		if restriction.ReturnDescription != "" {
			values = append(values, "Return restriction: "+restriction.ReturnDescription)
		}
	}
	for _, advancePurchase := range conditions.AdvancePurchase {
		message := "Advance-purchase booking conditions apply"
		switch advancePurchase.CheckType {
		case "0":
			message += "; book by " + advancePurchase.BookingDate.Format("2 Jan 2006")
			if advancePurchase.HasBookingTime {
				message += fmt.Sprintf(" at %02d:%02d", advancePurchase.BookingTime/60, advancePurchase.BookingTime%60)
			}
		case "1":
			message += fmt.Sprintf("; book at least %d hours before departure", advancePurchase.AdvanceHours)
		case "2":
			message += fmt.Sprintf("; book at least %d days before departure", advancePurchase.AdvanceDays)
		}
		if advancePurchase.TOCCode != "" {
			message += " for TOC " + advancePurchase.TOCCode
		}
		values = append(values, message)
	}
	for _, tocTicket := range conditions.TOCSpecificTickets {
		if tocTicket.Direction == "R" {
			continue
		}
		message := "TOC-specific ticket condition"
		if tocTicket.TOCType == "M" {
			message = "Ticket requires main TOC " + tocTicket.TOCCode
		} else if tocTicket.TOCCode != "" {
			message = "Ticket permits connecting TOC " + tocTicket.TOCCode + " when a main TOC is used"
		}
		if tocTicket.SleeperRequired {
			message += "; sleeper accommodation required"
		}
		if len(tocTicket.StockList) > 0 {
			message += "; stock type is restricted"
		}
		values = append(values, message)
	}
	return values
}

func fareRailConditionsMatch(ctx context.Context, catalogue *fareCatalogue, conditions *ctdf.FareRailConditions, journey *ctdf.Journey, item ctdf.JourneyPlanRouteItem) (bool, string) {
	travelTime, _ := fareItemValidationTimes(item)
	if travelTime.IsZero() {
		return true, "Rail restrictions depend on a journey date or time that is unavailable."
	}
	date := fareLocalJourneyTime(travelTime, journey)
	dateOnly := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	partialReasons := make([]string, 0)
	for _, condition := range conditions.AdvancePurchase {
		if !fareDateWithinRange(dateOnly, condition.ValidFrom, condition.ValidTo) {
			continue
		}
		if condition.TOCCode != "" {
			operatorMatches, operatorKnown := fareJourneyContainsTOC(journey, condition.TOCCode)
			if !operatorKnown {
				partialReasons = append(partialReasons, "An advance-purchase deadline depends on an operator code that is not linked to this journey.")
				continue
			}
			if !operatorMatches {
				continue
			}
		}
		timezone := ""
		if journey != nil {
			timezone = journey.DepartureTimezone
		}
		eligible, known := fareAdvancePurchaseEligible(condition, item.StartTime, timezone, time.Now())
		if !known {
			partialReasons = append(partialReasons, "An advance-purchase deadline could not be evaluated from the journey time.")
		} else if !eligible {
			return false, ""
		}
	}
	if len(conditions.TOCSpecificTickets) > 0 {
		eligible, known, stockUnresolved := fareTOCSpecificTicketMatches(journey, conditions.TOCSpecificTickets, dateOnly)
		if !known {
			partialReasons = append(partialReasons, "TOC-specific ticket conditions could not be checked against all journey operators.")
		} else if !eligible {
			return false, ""
		}
		if stockUnresolved {
			partialReasons = append(partialReasons, "A TOC-specific ticket depends on sleeper or rolling-stock details that are not present in the journey.")
		}
	}
	if conditions.RouteCode != "" && conditions.RouteCode != "00000" {
		if conditions.Route == nil {
			partialReasons = append(partialReasons, "The route description is missing for this fare's route code.")
		} else if journey == nil {
			partialReasons = append(partialReasons, "The route code could not be checked because journey calls are unavailable.")
		} else {
			for _, routeLocation := range conditions.Route.Locations {
				found := false
				known := true
				calls := fareItemCoveredCalls(item)
				for _, call := range calls {
					var matches, resolved bool
					if routeLocation.CRS != "" {
						matches, resolved = fareJourneyCallMatchesCRS(ctx, catalogue, call, routeLocation.CRS)
					} else {
						matches, resolved = fareJourneyCallMatchesNLC(ctx, call, routeLocation.NLC)
					}
					known = known && resolved
					found = found || matches
				}
				if routeLocation.IncludeExclude == "E" && found {
					return false, ""
				}
				if !known {
					partialReasons = append(partialReasons, "An explicit route location could not be resolved against the journey stops.")
				} else if routeLocation.IncludeExclude == "I" && !found {
					partialReasons = append(partialReasons, "An included route location is not present on this journey.")
				}
			}
		}
		partialReasons = append(partialReasons, "National Rail permitted-route rules are not available in RSPS5045 and have not been checked.")
	}
	for _, calendar := range conditions.TicketCalendars {
		if calendar.RouteCode != "" && calendar.RouteCode != conditions.RouteCode {
			continue
		}
		if !fareDateWithinRange(dateOnly, calendar.ValidFrom, calendar.ValidTo) || !fareRailDateBandsMatch(date, []ctdf.FareRailDateBand{{FromMonthDay: calendar.FromMonthDay, ToMonthDay: calendar.ToMonthDay, Days: calendar.Days}}) {
			continue
		}
		if calendar.Country != "" {
			partialReasons = append(partialReasons, "A ticket calendar is country-specific, but the journey's fare-location country is not known.")
			continue
		}
		switch calendar.Type {
		case "I":
			return false, ""
		case "D":
			partialReasons = append(partialReasons, "A ticket calendar marks this travel date as restricted.")
		case "S":
			partialReasons = append(partialReasons, "A supplement calendar applies to this ticket.")
		default:
			partialReasons = append(partialReasons, "The ticket calendar type is not recognized.")
		}
	}
	trainUID := ""
	joinedFareLeg := false
	if journey != nil {
		trainUID = strings.TrimSpace(journey.OtherIdentifiers["TrainUID"])
		joinedFareLeg = strings.HasPrefix(journey.PrimaryIdentifier, "fare-effective-")
	}
	for _, restriction := range conditions.Restrictions {
		if !fareDateWithinRange(dateOnly, restriction.ValidFrom, restriction.ValidTo) || !fareRailDateBandsMatch(date, restriction.DateBands) {
			continue
		}
		if len(restriction.RouteLocations) > 0 {
			applies, known := fareRestrictionRouteLocationsMatch(ctx, catalogue, journey, item, restriction.RouteLocations)
			if !known {
				partialReasons = append(partialReasons, "A rail restriction location could not be resolved against the journey stops.")
				continue
			}
			if !applies {
				continue
			}
		}
		if joinedFareLeg {
			if !restriction.ChangeAllowed {
				return false, ""
			}
			if len(restriction.AllowedChangeLocations) > 0 {
				allowed, known := fareRestrictionAllowedChangesMatch(ctx, catalogue, journey, restriction.AllowedChangeLocations)
				if !known {
					partialReasons = append(partialReasons, "A transfer location could not be checked against the restriction's allowed change locations.")
				} else if !allowed {
					return false, ""
				}
			}
		}
		partialReasons = append(partialReasons, restriction.Unsupported...)
		if restriction.OutwardMode != "P" && restriction.OutwardMode != "N" && len(restriction.TrainRestrictions) > 0 {
			partialReasons = append(partialReasons, "A rail restriction has an unsupported positive/negative mode.")
		}
		if len(restriction.TrainRestrictions) > 0 {
			trainMatched := false
			activeTrainRules := 0
			trainKnown := trainUID != ""
			trainRulesKnown := trainKnown && !joinedFareLeg
			if joinedFareLeg {
				trainKnown = false
				partialReasons = append(partialReasons, "A train restriction could not be confirmed across a fare leg joined from multiple scheduled journeys.")
			}
			for _, trainRestriction := range restriction.TrainRestrictions {
				if trainRestriction.OutRet == "R" || !fareRailDateBandsMatch(date, trainRestriction.DateBands) {
					continue
				}
				activeTrainRules++
				if trainRestriction.Sleeper {
					trainRulesKnown = false
					partialReasons = append(partialReasons, "A sleeper-only train restriction cannot be resolved from this timetable record.")
					continue
				}
				if trainRestriction.Quota {
					trainRulesKnown = false
					partialReasons = append(partialReasons, "A quota-controlled train restriction requires reservation inventory.")
					continue
				}
				if trainKnown && trainRestriction.TrainUID == trainUID {
					trainMatched = true
				}
			}
			if !trainKnown {
				partialReasons = append(partialReasons, "A train restriction could not be matched because the journey has no National Rail Train UID.")
			} else if trainRulesKnown && restriction.OutwardMode == "P" && activeTrainRules > 0 && !trainMatched {
				return false, ""
			} else if !joinedFareLeg && restriction.OutwardMode == "N" && trainMatched {
				return false, ""
			}
		}
		if len(restriction.TimeRestrictions) > 0 {
			for _, timeRestriction := range restriction.TimeRestrictions {
				if timeRestriction.OutRet == "R" || !fareRailDateBandsMatch(date, timeRestriction.DateBands) {
					continue
				}
				if timeRestriction.TimetableType == "A" {
					partialReasons = append(partialReasons, "A rail time restriction requires actual running times.")
					continue
				}
				if timeRestriction.TimetableType != "T" {
					partialReasons = append(partialReasons, "A rail time restriction has an unsupported timetable type.")
					continue
				}
				if timeRestriction.TrainType != "" {
					partialReasons = append(partialReasons, "A rail time restriction depends on a train sector type.")
					continue
				}
				if len(timeRestriction.TOCCodes) > 0 {
					operatorMatches, operatorKnown := fareJourneyOperatorMatches(journey, timeRestriction.TOCCodes)
					if !operatorKnown {
						partialReasons = append(partialReasons, "A rail time restriction depends on an operator code that is not linked to this journey.")
						continue
					}
					if !operatorMatches {
						continue
					}
				}
				matched, known := fareTimeRestrictionMatches(ctx, catalogue, journey, item, timeRestriction)
				if !known {
					partialReasons = append(partialReasons, "A station-specific rail time restriction could not be resolved to a fare location.")
					continue
				}
				if matched && joinedFareLeg {
					partialReasons = append(partialReasons, "A time restriction could not be confirmed across a fare leg joined from multiple scheduled journeys.")
					continue
				}
				if matched {
					if timeRestriction.MinimumFare {
						partialReasons = append(partialReasons, "A rail time restriction requires the applicable minimum fare.")
						continue
					}
					return false, ""
				}
			}
		}
	}
	return true, strings.Join(uniqueFareNames(partialReasons), " ")
}

func fareAdvancePurchaseEligible(condition ctdf.FareRailAdvancePurchase, departure time.Time, timezone string, now time.Time) (bool, bool) {
	if departure.IsZero() || now.IsZero() {
		return false, false
	}
	location := time.UTC
	if timezone != "" {
		if parsed, err := time.LoadLocation(timezone); err == nil {
			location = parsed
		}
	}
	departure = departure.In(location)
	now = now.In(location)
	var deadline time.Time
	switch condition.CheckType {
	case "0":
		if condition.BookingDate.IsZero() || !condition.HasBookingTime {
			return false, false
		}
		deadline = time.Date(condition.BookingDate.Year(), condition.BookingDate.Month(), condition.BookingDate.Day(), condition.BookingTime/60, condition.BookingTime%60, 0, 0, location)
	case "1":
		if condition.AdvanceHours <= 0 {
			return false, false
		}
		deadline = departure.Add(-time.Duration(condition.AdvanceHours) * time.Hour)
	case "2":
		if condition.AdvanceDays <= 0 || !condition.HasBookingTime {
			return false, false
		}
		bookingDay := departure.AddDate(0, 0, -condition.AdvanceDays)
		bookingHour, bookingMinute := condition.BookingTime/60, condition.BookingTime%60
		deadline = time.Date(bookingDay.Year(), bookingDay.Month(), bookingDay.Day(), bookingHour, bookingMinute, 0, 0, location)
	default:
		return false, false
	}
	return !now.After(deadline), true
}

func fareTOCSpecificTicketMatches(journey *ctdf.Journey, conditions []ctdf.FareRailTOCTicket, travelDate time.Time) (eligible, known, stockUnresolved bool) {
	if journey == nil {
		return false, false, false
	}
	active := make([]ctdf.FareRailTOCTicket, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Direction == "R" || (condition.Direction != "O" && condition.Direction != "B") || !fareDateWithinRange(travelDate, condition.ValidFrom, condition.ValidTo) {
			continue
		}
		active = append(active, condition)
	}
	if len(active) == 0 {
		return true, true, false
	}
	operatorRefs := fareJourneyOperatorRefs(journey)
	tocCodes := make(map[string]bool, len(operatorRefs))
	for _, operatorRef := range operatorRefs {
		operatorRef = strings.ToUpper(strings.TrimSpace(operatorRef))
		if strings.HasPrefix(operatorRef, "GB-TOC-") {
			tocCodes[strings.TrimPrefix(operatorRef, "GB-TOC-")] = true
		} else if operatorRef != "" {
			return false, false, false
		}
	}
	if len(tocCodes) == 0 {
		return false, false, false
	}
	mainTOCs := make(map[string]bool)
	connectingTOCs := make(map[string]bool)
	allConnectingTOCs := false
	for _, condition := range active {
		code := strings.ToUpper(strings.TrimSpace(condition.TOCCode))
		switch condition.TOCType {
		case "M":
			if code == "" {
				return false, false, false
			}
			mainTOCs[code] = true
		case "C":
			if code == "" {
				allConnectingTOCs = true
			} else {
				connectingTOCs[code] = true
			}
		default:
			return false, false, false
		}
		if condition.SleeperRequired || len(condition.StockList) > 0 {
			stockUnresolved = true
		}
	}
	mainUsed := false
	for code := range tocCodes {
		if mainTOCs[code] {
			mainUsed = true
		}
	}
	if len(mainTOCs) > 0 && !mainUsed {
		return false, true, stockUnresolved
	}
	for code := range tocCodes {
		if mainTOCs[code] {
			continue
		}
		if (!connectingTOCs[code] && !allConnectingTOCs) || !mainUsed {
			return false, true, stockUnresolved
		}
	}
	return true, true, stockUnresolved
}

func fareJourneyOperatorRefs(journey *ctdf.Journey) []string {
	if journey == nil {
		return nil
	}
	if refs := journey.OtherIdentifiers["FareOperatorRefs"]; refs != "" {
		return strings.Split(refs, ",")
	}
	if journey.OperatorRef != "" {
		return []string{journey.OperatorRef}
	}
	return nil
}

func fareJourneyContainsTOC(journey *ctdf.Journey, tocCode string) (bool, bool) {
	operatorRefs := fareJourneyOperatorRefs(journey)
	if len(operatorRefs) == 0 {
		return false, false
	}
	found := false
	for _, operatorRef := range operatorRefs {
		operatorRef = strings.ToUpper(strings.TrimSpace(operatorRef))
		if !strings.HasPrefix(operatorRef, "GB-TOC-") {
			if operatorRef != "" {
				return false, false
			}
			continue
		}
		found = found || strings.TrimPrefix(operatorRef, "GB-TOC-") == strings.ToUpper(strings.TrimSpace(tocCode))
	}
	return found, true
}

func fareRestrictionRouteLocationsMatch(ctx context.Context, catalogue *fareCatalogue, journey *ctdf.Journey, item ctdf.JourneyPlanRouteItem, locations []string) (bool, bool) {
	if len(locations) == 0 {
		return true, true
	}
	if journey == nil {
		return false, false
	}
	known := true
	calls := fareRestrictionRelevantCalls(journey, item)
	for _, location := range locations {
		for _, call := range calls {
			matches, resolved := fareJourneyCallMatchesCRS(ctx, catalogue, call, location)
			if !resolved {
				known = false
				continue
			}
			if matches {
				return true, true
			}
		}
	}
	return false, known
}

func fareRestrictionRelevantCalls(journey *ctdf.Journey, item ctdf.JourneyPlanRouteItem) []*ctdf.JourneyCall {
	calls := fareItemCoveredCalls(item)
	if journey != nil && journey.OtherIdentifiers["FareChangeCallIndexes"] != "" {
		for _, value := range strings.Split(journey.OtherIdentifiers["FareChangeCallIndexes"], ",") {
			index, err := strconv.Atoi(value)
			if err == nil {
				if call := journey.StopCall(index); call != nil {
					calls = append(calls, call)
				}
			}
		}
	}
	return calls
}

func fareItemCoveredCalls(item ctdf.JourneyPlanRouteItem) []*ctdf.JourneyCall {
	start, end, ok := fareItemLegRange(item)
	if !ok {
		return nil
	}
	calls := make([]*ctdf.JourneyCall, 0, end-start+1)
	for legIndex := start; legIndex < end; legIndex++ {
		origin, destination := item.Journey.LegEndpoints(legIndex)
		if origin == nil || destination == nil {
			continue
		}
		if len(calls) == 0 || calls[len(calls)-1] != origin {
			calls = append(calls, origin)
		}
		calls = append(calls, destination)
	}
	return calls
}

func fareRestrictionAllowedChangesMatch(ctx context.Context, catalogue *fareCatalogue, journey *ctdf.Journey, allowedLocations []string) (bool, bool) {
	if journey == nil || journey.OtherIdentifiers["FareChangeCallIndexes"] == "" {
		return false, false
	}
	indexes := strings.Split(journey.OtherIdentifiers["FareChangeCallIndexes"], ",")
	if len(indexes) == 0 {
		return false, false
	}
	for _, value := range indexes {
		index, err := strconv.Atoi(value)
		if err != nil {
			return false, false
		}
		call := journey.StopCall(index)
		if call == nil {
			return false, false
		}
		locationResolved := false
		locationAllowed := false
		for _, allowedLocation := range allowedLocations {
			matches, resolved := fareJourneyCallMatchesCRS(ctx, catalogue, call, allowedLocation)
			if !resolved {
				continue
			}
			locationResolved = true
			if matches {
				locationAllowed = true
				break
			}
		}
		if !locationResolved {
			return false, false
		}
		if !locationAllowed {
			return false, true
		}
	}
	return true, true
}

func fareDateWithinRange(date, from, to time.Time) bool {
	if date.IsZero() {
		return false
	}
	dateOnly := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if !from.IsZero() {
		start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
		if dateOnly.Before(start) {
			return false
		}
	}
	if !to.IsZero() {
		end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		if dateOnly.After(end) {
			return false
		}
	}
	return true
}

func fareRailDateBandsMatch(date time.Time, bands []ctdf.FareRailDateBand) bool {
	if len(bands) == 0 {
		return true
	}
	for _, band := range bands {
		from, fromErr := time.Parse("0102", band.FromMonthDay)
		to, toErr := time.Parse("0102", band.ToMonthDay)
		if fromErr != nil || toErr != nil {
			continue
		}
		candidate := time.Date(2000, date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		from = time.Date(2000, from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
		to = time.Date(2000, to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		inDateRange := !candidate.Before(from) && !candidate.After(to)
		if from.After(to) {
			inDateRange = !candidate.Before(from) || !candidate.After(to)
		}
		if !inDateRange {
			continue
		}
		if band.Days != "" {
			weekdayIndex := (int(date.Weekday()) + 6) % 7
			if len(band.Days) < 7 || band.Days[weekdayIndex] != 'Y' {
				continue
			}
		}
		return true
	}
	return false
}

func fareJourneyOperatorMatches(journey *ctdf.Journey, codes []string) (bool, bool) {
	operatorRefs := fareJourneyOperatorRefs(journey)
	if len(operatorRefs) == 0 {
		return false, false
	}
	found := false
	known := true
	for _, operatorRef := range operatorRefs {
		operatorRef = strings.ToUpper(strings.TrimSpace(operatorRef))
		if strings.HasPrefix(operatorRef, "GB-TOC-") {
			operatorCode := strings.TrimPrefix(operatorRef, "GB-TOC-")
			for _, code := range codes {
				if operatorCode == strings.ToUpper(strings.TrimSpace(code)) {
					found = true
				}
			}
			continue
		}
		matchedRef := false
		for _, code := range codes {
			code = strings.ToUpper(strings.TrimSpace(code))
			if code != "" && (operatorRef == code || strings.HasSuffix(operatorRef, ":"+code) || strings.HasSuffix(operatorRef, "-"+code)) {
				found = true
				matchedRef = true
			}
		}
		if !matchedRef {
			known = false
		}
	}
	return found, known
}

func fareTimeRestrictionMatches(ctx context.Context, catalogue *fareCatalogue, journey *ctdf.Journey, item ctdf.JourneyPlanRouteItem, restriction ctdf.FareRailTimeRestriction) (bool, bool) {
	if journey == nil {
		return false, false
	}
	calls := make([]*ctdf.JourneyCall, 0)
	if restriction.Event == "V" {
		changeIndexes, valid := fareJourneyChangeIndexes(journey)
		if !valid {
			return false, false
		}
		for _, index := range changeIndexes {
			if call := journey.StopCall(index); call != nil {
				calls = append(calls, call)
			}
		}
		if len(calls) == 0 {
			return false, true
		}
	} else if restriction.Event == "A" || restriction.Event == "D" {
		calls = fareItemCoveredCalls(item)
	} else {
		return false, false
	}
	known := false
	timeApplicable := restriction.LocationCRS == ""
	timeFound := false
	for _, call := range calls {
		if call == nil {
			continue
		}
		if restriction.LocationCRS != "" {
			matches, locationKnown := fareJourneyCallMatchesCRS(ctx, catalogue, call, restriction.LocationCRS)
			if !locationKnown {
				continue
			}
			known = true
			if !matches {
				continue
			}
			timeApplicable = true
		} else {
			known = true
		}
		candidates := []time.Time{}
		switch restriction.Event {
		case "A":
			candidates = append(candidates, call.Arrival)
		case "D":
			candidates = append(candidates, call.Departure)
		case "V":
			candidates = append(candidates, call.Arrival, call.Departure)
		}
		hasCandidateTime := false
		for _, candidate := range candidates {
			if candidate.IsZero() {
				continue
			}
			hasCandidateTime = true
			timeFound = true
			match, hasTime := fareClockWithin(candidate, journey.DepartureTimezone, restriction.TimeFrom, restriction.TimeTo)
			if !hasTime {
				return false, false
			}
			if match {
				return true, true
			}
		}
		if restriction.LocationCRS != "" && !hasCandidateTime {
			return false, false
		}
	}
	if timeApplicable && !timeFound {
		return false, false
	}
	return false, known
}

func fareJourneyChangeIndexes(journey *ctdf.Journey) ([]int, bool) {
	if journey == nil || journey.OtherIdentifiers["FareChangeCallIndexes"] == "" {
		return nil, journey != nil
	}
	values := strings.Split(journey.OtherIdentifiers["FareChangeCallIndexes"], ",")
	indexes := make([]int, 0, len(values))
	for _, value := range values {
		index, err := strconv.Atoi(value)
		if err != nil || index < 0 || index >= len(journey.Calls) {
			return nil, false
		}
		indexes = append(indexes, index)
	}
	return indexes, true
}

func fareJourneyCallMatchesCRS(ctx context.Context, catalogue *fareCatalogue, call *ctdf.JourneyCall, crs string) (bool, bool) {
	if call == nil || call.StopRef == "" || crs == "" {
		return false, false
	}
	crs = strings.ToUpper(strings.TrimSpace(crs))
	if values, exists := catalogue.stopCRSCodes[call.StopRef]; exists {
		if len(values) == 0 {
			return false, false
		}
		return values[crs], true
	}
	if call.Stop == nil {
		var stop ctdf.Stop
		if database.GetCollection("stops").FindOne(ctx, bson.M{"primaryidentifier": call.StopRef}).Decode(&stop) != nil {
			return false, false
		}
		call.Stop = &stop
	}
	identifiers := make(map[string]bool, len(call.Stop.OtherIdentifiers))
	for _, otherIdentifier := range call.Stop.OtherIdentifiers {
		upper := strings.ToUpper(strings.TrimSpace(otherIdentifier))
		if strings.HasPrefix(upper, "GB-CRS-") {
			identifiers[strings.TrimPrefix(upper, "GB-CRS-")] = true
		} else if separator := strings.LastIndex(upper, ":"); separator >= 0 {
			code := strings.TrimSpace(upper[separator+1:])
			isCRSCode := len(code) == 3
			for _, character := range code {
				if character < 'A' || character > 'Z' {
					isCRSCode = false
				}
			}
			if isCRSCode {
				identifiers[code] = true
			}
		}
	}
	catalogue.stopCRSCodes[call.StopRef] = identifiers
	if len(identifiers) == 0 {
		return false, false
	}
	return identifiers[crs], true
}

func fareJourneyCallMatchesNLC(ctx context.Context, call *ctdf.JourneyCall, nlc string) (bool, bool) {
	if call == nil || call.StopRef == "" || nlc == "" {
		return false, false
	}
	if call.Stop == nil {
		var stop ctdf.Stop
		if database.GetCollection("stops").FindOne(ctx, bson.M{"primaryidentifier": call.StopRef}).Decode(&stop) != nil {
			return false, false
		}
		call.Stop = &stop
	}
	needle := strings.ToUpper(strings.TrimSpace(nlc))
	for _, identifier := range call.Stop.OtherIdentifiers {
		identifier = strings.ToUpper(strings.TrimSpace(identifier))
		if strings.HasPrefix(identifier, "GB-NLC-") && strings.TrimPrefix(identifier, "GB-NLC-") == needle {
			return true, true
		}
		if strings.HasSuffix(identifier, ":"+needle) {
			return true, true
		}
	}
	return false, true
}

func fareClockWithin(value time.Time, timezone string, from, to int) (bool, bool) {
	if value.IsZero() {
		return false, false
	}
	if timezone != "" {
		if location, err := time.LoadLocation(timezone); err == nil {
			value = value.In(location)
		}
	}
	minutes := value.Hour()*60 + value.Minute()
	if from <= to {
		return minutes >= from && minutes <= to, true
	}
	return minutes >= from || minutes <= to, true
}

func fareRiderCondition(rider ctdf.FareRiderCategory) string {
	label := rider.Name
	if rider.MinimumAge != nil && rider.MaximumAge != nil {
		label += fmt.Sprintf(" (ages %d–%d)", *rider.MinimumAge, *rider.MaximumAge)
	} else if rider.MinimumAge != nil {
		label += fmt.Sprintf(" (age %d+)", *rider.MinimumAge)
	} else if rider.MaximumAge != nil {
		label += fmt.Sprintf(" (up to age %d)", *rider.MaximumAge)
	}
	if rider.ProofRequired != "" {
		label += "; proof required: " + rider.ProofRequired
	}
	return "Passenger eligibility: " + label
}

func selectedFareRailcards(catalogue *fareCatalogue, selected map[string]bool) []ctdf.FareRiderCategory {
	values := make([]ctdf.FareRiderCategory, 0)
	for ref := range selected {
		rider, exists := catalogue.riders[ref]
		if exists && strings.EqualFold(rider.UserType, "railcard") {
			values = append(values, rider)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].PrimaryIdentifier < values[j].PrimaryIdentifier
	})
	return values
}

func fareRiderIsAdult(ref string, catalogue *fareCatalogue) bool {
	rider, exists := catalogue.riders[ref]
	return exists && (strings.EqualFold(rider.UserType, "adult") || strings.EqualFold(rider.Name, "adult"))
}

func fareRailTicketConditions(ticket ctdf.FareRailTicket) []string {
	conditions := make([]string, 0, 5)
	if ticket.Validity != nil {
		validity := ticket.Validity
		if validity.OutwardDescription != "" {
			conditions = append(conditions, "Outward validity: "+validity.OutwardDescription)
		} else if validity.OutwardDays > 0 || validity.OutwardMonths > 0 {
			conditions = append(conditions, fmt.Sprintf("Outward validity: %d days and %d months", validity.OutwardDays, validity.OutwardMonths))
		}
		if validity.ReturnDescription != "" {
			conditions = append(conditions, "Return validity: "+validity.ReturnDescription)
		} else if validity.ReturnDays > 0 || validity.ReturnMonths > 0 {
			conditions = append(conditions, fmt.Sprintf("Return validity: %d days and %d months", validity.ReturnDays, validity.ReturnMonths))
		}
		if validity.ReturnAfterDays > 0 || validity.ReturnAfterMonths > 0 || validity.ReturnAfterWeekday != "" {
			conditions = append(conditions, "Return travel is subject to a minimum return period")
		}
		if validity.BreakOutwardAllowed || validity.BreakReturnAllowed {
			conditions = append(conditions, "Break of journey is permitted")
		} else if validity.Description != "" {
			conditions = append(conditions, "Ticket validity: "+validity.Description)
		}
	}
	switch ticket.Reservation {
	case "O":
		conditions = append(conditions, "Reservation required for the outward journey")
	case "R":
		conditions = append(conditions, "Reservation required for the return journey")
	case "B":
		conditions = append(conditions, "Reservation required for both journeys")
	case "E":
		conditions = append(conditions, "Reservation required for the outward or return journey")
	}
	if ticket.MinimumPassengers > 0 || ticket.MaximumPassengers > 0 {
		switch {
		case ticket.MinimumPassengers > 0 && ticket.MaximumPassengers > 0:
			conditions = append(conditions, fmt.Sprintf("Ticket requires %d to %d passengers", ticket.MinimumPassengers, ticket.MaximumPassengers))
		case ticket.MinimumPassengers > 0:
			conditions = append(conditions, fmt.Sprintf("Ticket requires at least %d passengers", ticket.MinimumPassengers))
		case ticket.MaximumPassengers > 0:
			conditions = append(conditions, fmt.Sprintf("Ticket allows at most %d passengers", ticket.MaximumPassengers))
		}
	}
	if !ticket.LastValidDay.IsZero() {
		conditions = append(conditions, "Last valid travel date: "+ticket.LastValidDay.Format("2006-01-02"))
	}
	return conditions
}

func fareRailTicketAllowsPassengerCount(ticket ctdf.FareRailTicket, passengerCount int) bool {
	return passengerCount >= ticket.MinimumPassengers &&
		(ticket.MaximumPassengers == 0 || passengerCount <= ticket.MaximumPassengers)
}

func fareOfferValidAt(offer ctdf.FareOffer, travelTime time.Time) bool {
	if travelTime.IsZero() {
		return true
	}
	if !offer.SalesValidFrom.IsZero() && travelTime.Before(offer.SalesValidFrom) {
		return false
	}
	if !offer.SalesValidToExclusive.IsZero() {
		return travelTime.Before(offer.SalesValidToExclusive)
	}
	if offer.SalesValidTo.IsZero() {
		return true
	}
	return !travelTime.After(offer.SalesValidTo)
}

func fareOfferValidForProduct(offer ctdf.FareOffer, travelTime time.Time, product ctdf.FareProduct, journey *ctdf.Journey) bool {
	if product.RailTicket == nil {
		return fareOfferValidAt(offer, travelTime)
	}
	if travelTime.IsZero() {
		return true
	}
	travelDate := fareRailCalendarDate(travelTime, journey)
	if !offer.SalesValidFrom.IsZero() {
		validFrom := time.Date(offer.SalesValidFrom.Year(), offer.SalesValidFrom.Month(), offer.SalesValidFrom.Day(), 0, 0, 0, 0, time.UTC)
		if travelDate.Before(validFrom) {
			return false
		}
	}
	if offer.SalesValidTo.IsZero() {
		return true
	}
	validTo := time.Date(offer.SalesValidTo.Year(), offer.SalesValidTo.Month(), offer.SalesValidTo.Day(), 0, 0, 0, 0, time.UTC)
	return !travelDate.After(validTo)
}

func fareRailCalendarDate(travelTime time.Time, journey *ctdf.Journey) time.Time {
	local := fareLocalJourneyTime(travelTime, journey)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

func fareLocalJourneyTime(travelTime time.Time, journey *ctdf.Journey) time.Time {
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		location = time.UTC
	}
	if journey != nil && journey.DepartureTimezone != "" {
		if journeyLocation, loadErr := time.LoadLocation(journey.DepartureTimezone); loadErr == nil {
			location = journeyLocation
		}
	}
	if location == nil {
		location = time.UTC
	}
	return travelTime.In(location)
}

func fareTimeframeMatches(groupRef string, travelTime time.Time, journey *ctdf.Journey, call *ctdf.JourneyCall, timeframes []ctdf.FareTimeframe, stopTimezones map[string]string) (bool, bool) {
	if groupRef == "" {
		return true, true
	}
	if travelTime.IsZero() || journey == nil {
		return false, false
	}
	location := travelTime.Location()
	timezone := journey.DepartureTimezone
	if call != nil && call.Stop != nil && call.Stop.Timezone != "" {
		timezone = call.Stop.Timezone
	} else if call != nil && stopTimezones[call.StopRef] != "" {
		timezone = stopTimezones[call.StopRef]
	}
	if timezone != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return false, false
		}
		location = loaded
	}
	localTime := travelTime.In(location)
	localDate := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, time.UTC)
	clockSeconds := int64(localTime.Hour()*3600 + localTime.Minute()*60 + localTime.Second())
	foundGroup := false
	serviceSpecificUnknown := false
	for _, timeframe := range timeframes {
		if timeframe.GroupRef != groupRef {
			continue
		}
		foundGroup = true
		if timeframe.ServiceRef != "" {
			if timeframe.Availability == nil {
				serviceSpecificUnknown = true
				continue
			}
			if fareTimeframeDateListed(timeframe.RemovedDates, localTime) {
				continue
			}
			if !fareTimeframeDateListed(timeframe.AddedDates, localTime) && !timeframe.Availability.MatchDate(localDate) {
				continue
			}
		}
		if clockSeconds >= timeframe.StartTimeSecond && clockSeconds < timeframe.EndTimeSecond {
			return true, true
		}
	}
	if !foundGroup {
		return false, false
	}
	if serviceSpecificUnknown {
		return false, false
	}
	return false, true
}

func fareTimeframeDateListed(dates []time.Time, date time.Time) bool {
	for _, candidate := range dates {
		if candidate.Year() == date.Year() && candidate.Month() == date.Month() && candidate.Day() == date.Day() {
			return true
		}
	}
	return false
}

func activeFareVersionHead(ctx context.Context, scheduleDatasetID string) (*ctdf.FareVersionHead, error) {
	var direct ctdf.FareVersionHead
	err := database.GetCollection("fare_versions").FindOne(ctx, bson.M{"datasetid": scheduleDatasetID}).Decode(&direct)
	if err == nil && direct.ActiveVersionID != "" {
		return &direct, nil
	}
	if err != nil && err != mongo.ErrNoDocuments {
		return nil, err
	}
	cursor, err := database.GetCollection("fare_versions").Find(ctx, bson.M{"activeversionid": bson.M{"$exists": true, "$ne": ""}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var match *ctdf.FareVersionHead
	for cursor.Next(ctx) {
		var candidate ctdf.FareVersionHead
		if err := cursor.Decode(&candidate); err != nil {
			return nil, err
		}
		pattern := candidate.LinkedScheduleDatasetID
		if pattern == "" {
			continue
		}
		matched, err := path.Match(pattern, scheduleDatasetID)
		if err != nil {
			return nil, fmt.Errorf("invalid linked schedule pattern %q: %w", pattern, err)
		}
		if !matched {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("multiple fare catalogues are linked to schedule %s", scheduleDatasetID)
		}
		copy := candidate
		match = &copy
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return match, nil
}

func (catalogue *fareCatalogue) serviceNetworksFor(ctx context.Context, serviceRef string) []string {
	if serviceRef == "" {
		return nil
	}
	if networks, exists := catalogue.serviceNetworks[serviceRef]; exists {
		return networks
	}
	var networks []string
	var service ctdf.Service
	if database.GetCollection("services").FindOne(ctx, bson.M{"primaryidentifier": serviceRef}).Decode(&service) == nil {
		if service.NetworkRef != "" {
			networks = append(networks, service.NetworkRef)
		}
	}
	for _, routeNetwork := range catalogue.routeNetworks {
		if routeNetwork.RouteRef == serviceRef && !containsFareRef(networks, routeNetwork.NetworkRef) {
			networks = append(networks, routeNetwork.NetworkRef)
		}
	}
	catalogue.serviceNetworks[serviceRef] = networks
	return networks
}

func readFareRouteNetworks(ctx context.Context, filter bson.M) []ctdf.FareRouteNetwork {
	values := make([]ctdf.FareRouteNetwork, 0)
	if err := readFareCollection(ctx, "fare_route_networks", filter, &values); err != nil {
		return nil
	}
	return values
}

func readFareTransferRules(ctx context.Context, filter bson.M) []ctdf.FareTransferRule {
	values := make([]ctdf.FareTransferRule, 0)
	if err := readFareCollection(ctx, "fare_transfer_rules", filter, &values); err != nil {
		return nil
	}
	return values
}

func readFareEffectiveJoins(ctx context.Context, filter bson.M) []ctdf.FareEffectiveLegJoin {
	values := make([]ctdf.FareEffectiveLegJoin, 0)
	if err := readFareCollection(ctx, "fare_effective_leg_joins", filter, &values); err != nil {
		return nil
	}
	return values
}

func readFareCaps(ctx context.Context, filter bson.M) []ctdf.FareCap {
	cursor, err := database.GetCollection("fare_caps").Find(ctx, filter, options.Find().SetLimit(1))
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)
	values := make([]ctdf.FareCap, 0, 1)
	if err := cursor.All(ctx, &values); err != nil {
		return nil
	}
	return values
}

func addFareRef(values map[string]bool, ref string) {
	if ref != "" {
		values[ref] = true
	}
}

func sortedFareRefs(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for ref := range values {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func fareFilterAnd(base bson.M, clauses ...bson.M) bson.M {
	filter := bson.M{}
	for key, value := range base {
		filter[key] = value
	}
	if len(clauses) > 0 {
		filter["$and"] = clauses
	}
	return filter
}

func fareIdentifierFilter(base bson.M, refs []string) bson.M {
	return fareIdentifierFieldFilter(base, "primaryidentifier", refs)
}

func fareIdentifierFieldFilter(base bson.M, field string, refs []string) bson.M {
	filter := bson.M{}
	for key, value := range base {
		filter[key] = value
	}
	filter[field] = bson.M{"$in": refs}
	return filter
}

func fareRefFieldMatch(field string, refs []string) bson.M {
	clauses := bson.A{bson.M{field: bson.M{"$exists": false}}}
	if len(refs) > 0 {
		clauses = append(clauses, bson.M{field: bson.M{"$in": refs}})
	}
	return bson.M{"$or": clauses}
}

func fareEmptyRefField(field string) bson.M {
	return fareRefFieldMatch(field, []string{""})
}

func fareLocationLookupFilter(base bson.M, refs, stopRefs, stageNumbers []string) bson.M {
	clauses := make(bson.A, 0, 3)
	if len(refs) > 0 {
		clauses = append(clauses, bson.M{"primaryidentifier": bson.M{"$in": refs}})
	}
	if len(stopRefs) > 0 {
		clauses = append(clauses, bson.M{"stoprefs": bson.M{"$in": stopRefs}})
	}
	if len(stageNumbers) > 0 {
		clauses = append(clauses, bson.M{"$and": bson.A{
			bson.M{"type": ctdf.FareLocationTypeStage},
			bson.M{"sourceidentifier": bson.M{"$in": stageNumbers}},
		}})
	}
	if len(clauses) == 0 {
		clauses = append(clauses, bson.M{"primaryidentifier": bson.M{"$in": []string{}}})
	}
	return fareFilterAnd(base, bson.M{"$or": clauses})
}

func loadFareLocationParents(ctx context.Context, versionFilter bson.M, locations map[string]ctdf.FareLocation) bool {
	for depth := 0; depth < 16; depth++ {
		missing := map[string]bool{}
		for _, location := range locations {
			if location.ParentRef != "" {
				if _, exists := locations[location.ParentRef]; !exists {
					missing[location.ParentRef] = true
				}
			}
		}
		if len(missing) == 0 {
			return true
		}
		parents := readFareLocations(ctx, fareIdentifierFilter(versionFilter, sortedFareRefs(missing)))
		if parents == nil {
			return false
		}
		for ref, location := range parents {
			locations[ref] = location
		}
		// Missing parent IDs should not cause an unbounded retry when the
		// catalogue contains a broken reference.
		for ref := range missing {
			if _, exists := locations[ref]; !exists {
				locations[ref] = ctdf.FareLocation{}
			}
		}
	}
	return true
}

func fareRiderFilter(base bson.M, refs []string, requested string) bson.M {
	clauses := bson.A{bson.M{"isdefault": true}}
	if len(refs) > 0 {
		clauses = append(clauses, bson.M{"primaryidentifier": bson.M{"$in": refs}})
	}
	if requested = strings.TrimSpace(requested); requested != "" {
		pattern := "^" + regexp.QuoteMeta(requested) + "$"
		clauses = append(clauses, bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$regex": pattern, "$options": "i"}},
			bson.M{"sourceidentifier": bson.M{"$regex": pattern, "$options": "i"}},
			bson.M{"name": bson.M{"$regex": pattern, "$options": "i"}},
		}})
	}
	return fareFilterAnd(base, bson.M{"$or": clauses})
}

func fareMediaFilter(base bson.M, refs []string, requested string) bson.M {
	clauses := bson.A{}
	if len(refs) > 0 {
		clauses = append(clauses, bson.M{"primaryidentifier": bson.M{"$in": refs}})
	}
	if requested = strings.TrimSpace(requested); requested != "" {
		pattern := "^" + regexp.QuoteMeta(requested) + "$"
		clauses = append(clauses, bson.M{"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$regex": pattern, "$options": "i"}},
			bson.M{"sourceidentifier": bson.M{"$regex": pattern, "$options": "i"}},
			bson.M{"name": bson.M{"$regex": pattern, "$options": "i"}},
			bson.M{"type": bson.M{"$regex": pattern, "$options": "i"}},
		}})
	}
	if len(clauses) == 0 {
		clauses = append(clauses, bson.M{"primaryidentifier": bson.M{"$in": []string{}}})
	}
	return fareFilterAnd(base, bson.M{"$or": clauses})
}

func fareTransferFilter(base bson.M, legGroupRefs []string) bson.M {
	refs := append([]string{""}, legGroupRefs...)
	return fareFilterAnd(base, bson.M{"$or": bson.A{
		fareRefFieldMatch("fromleggroupref", refs),
		fareRefFieldMatch("toleggroupref", refs),
	}})
}

func fareEffectiveJoinFilter(base bson.M, stopRefs, networkRefs []string) bson.M {
	clauses := bson.A{bson.M{"$and": bson.A{
		fareEmptyRefField("fromnetworkref"), fareEmptyRefField("tonetworkref"),
		fareEmptyRefField("fromstopref"), fareEmptyRefField("tostopref"),
	}}}
	if len(stopRefs) > 0 {
		clauses = append(clauses, bson.M{
			"fromstopref": bson.M{"$in": stopRefs}, "tostopref": bson.M{"$in": stopRefs},
		})
	}
	if len(networkRefs) > 0 {
		clauses = append(clauses, bson.M{
			"fromnetworkref": bson.M{"$in": networkRefs}, "tonetworkref": bson.M{"$in": networkRefs},
		})
	}
	return fareFilterAnd(base, bson.M{"$or": clauses})
}

func containsFareRef(values []string, ref string) bool {
	for _, value := range values {
		if value == ref {
			return true
		}
	}
	return false
}

func fareRouteMatches(routeRef, serviceRef string, service ctdf.Service, serviceFound bool) bool {
	if routeRef == "" || routeRef == serviceRef {
		return true
	}
	if serviceRef != "" && strings.HasSuffix(serviceRef, ":"+routeRef) {
		return true
	}
	localRef := routeRef
	if index := strings.LastIndex(localRef, ":"); index >= 0 && index+1 < len(localRef) {
		localRef = localRef[index+1:]
	}
	if serviceRef != "" && strings.HasSuffix(serviceRef, ":"+localRef) {
		return true
	}
	if serviceFound {
		if service.ServiceName == routeRef || service.ServiceName == localRef {
			return true
		}
		for _, identifier := range service.OtherIdentifiers {
			if identifier == routeRef || identifier == localRef || strings.HasSuffix(identifier, ":"+localRef) {
				return true
			}
		}
	}
	return false
}

func fareItineraryEndpoints(items []ctdf.JourneyPlanRouteItem) (ctdf.JourneyPlanRouteItem, ctdf.JourneyPlanRouteItem, bool) {
	if len(items) == 0 || items[0].Journey == nil || items[len(items)-1].Journey == nil {
		return ctdf.JourneyPlanRouteItem{}, ctdf.JourneyPlanRouteItem{}, false
	}
	first, last := items[0], items[len(items)-1]
	firstOrigin, _, _, _, firstOK := fareItemEndpointCalls(first)
	_, lastDestination, _, _, lastOK := fareItemEndpointCalls(last)
	if !firstOK || !lastOK || firstOrigin == nil || lastDestination == nil {
		return ctdf.JourneyPlanRouteItem{}, ctdf.JourneyPlanRouteItem{}, false
	}
	return first, last, true
}

func readFareLocations(ctx context.Context, filter bson.M) map[string]ctdf.FareLocation {
	var values []ctdf.FareLocation
	if err := readFareCollection(ctx, "fare_locations", filter, &values); err != nil {
		return nil
	}
	result := make(map[string]ctdf.FareLocation, len(values))
	for _, value := range values {
		result[value.PrimaryIdentifier] = value
	}
	return result
}

func readFareProducts(ctx context.Context, filter bson.M) map[string]ctdf.FareProduct {
	var values []ctdf.FareProduct
	if err := readFareCollection(ctx, "fare_products", filter, &values); err != nil {
		return nil
	}
	result := make(map[string]ctdf.FareProduct, len(values))
	for _, value := range values {
		result[value.PrimaryIdentifier] = value
	}
	return result
}

func readFareRiders(ctx context.Context, filter bson.M) map[string]ctdf.FareRiderCategory {
	var values []ctdf.FareRiderCategory
	if err := readFareCollection(ctx, "fare_rider_categories", filter, &values); err != nil {
		return nil
	}
	result := make(map[string]ctdf.FareRiderCategory, len(values))
	for _, value := range values {
		result[value.PrimaryIdentifier] = value
	}
	return result
}

func readFareMedia(ctx context.Context, filter bson.M) map[string]ctdf.FareMedia {
	var values []ctdf.FareMedia
	if err := readFareCollection(ctx, "fare_media", filter, &values); err != nil {
		return nil
	}
	result := make(map[string]ctdf.FareMedia, len(values))
	for _, value := range values {
		result[value.PrimaryIdentifier] = value
	}
	return result
}

func resolveFareRiderRefs(catalogue *fareCatalogue, requested string) (map[string]bool, string, bool) {
	selected := make(map[string]bool)
	requested = strings.TrimSpace(requested)
	assumptions := make([]string, 0)
	if requested != "" {
		for ref, rider := range catalogue.riders {
			if strings.EqualFold(requested, ref) || strings.EqualFold(requested, rider.SourceIdentifier) || strings.EqualFold(requested, rider.Name) {
				selected[ref] = true
				assumptions = append(assumptions, rider.Name)
			}
		}
		if len(selected) == 0 {
			return selected, "", false
		}
		return selected, "Passenger category: " + strings.Join(uniqueFareNames(assumptions), ", ") + " (selected).", true
	}
	for ref, rider := range catalogue.riders {
		if rider.IsDefault {
			selected[ref] = true
			assumptions = append(assumptions, rider.Name)
		}
	}
	if len(selected) == 0 {
		return selected, "", true
	}
	assumption := " (default fare category assumed)."
	for ref := range selected {
		rider := catalogue.riders[ref]
		if !strings.EqualFold(rider.UserType, "adult") && !strings.EqualFold(rider.Name, "adult") {
			return selected, "Passenger category: " + strings.Join(uniqueFareNames(assumptions), ", ") + assumption, true
		}
	}
	if len(selected) > 0 {
		assumption = " (adult fare assumed)."
	}
	return selected, "Passenger category: " + strings.Join(uniqueFareNames(assumptions), ", ") + assumption, true
}

func resolveFareMediaRefs(catalogue *fareCatalogue, requested string) (map[string]bool, bool) {
	selected := make(map[string]bool)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return selected, true
	}
	for ref, media := range catalogue.media {
		if strings.EqualFold(requested, ref) || strings.EqualFold(requested, media.SourceIdentifier) || strings.EqualFold(requested, media.Name) || strings.EqualFold(requested, string(media.Type)) {
			selected[ref] = true
		}
	}
	return selected, len(selected) > 0
}

func uniqueFareNames(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func readFareOffers(ctx context.Context, filter bson.M) map[string]ctdf.FareOffer {
	var values []ctdf.FareOffer
	if err := readFareCollection(ctx, "fare_offers", filter, &values); err != nil {
		return nil
	}
	result := make(map[string]ctdf.FareOffer, len(values))
	for _, value := range values {
		result[value.PrimaryIdentifier] = value
	}
	return result
}

func readFareTimeframes(ctx context.Context, filter bson.M) []ctdf.FareTimeframe {
	values := make([]ctdf.FareTimeframe, 0)
	if err := readFareCollection(ctx, "fare_timeframes", filter, &values); err != nil {
		return nil
	}
	return values
}

func readFareStopTimezones(ctx context.Context, stopRefs []string) map[string]string {
	result := make(map[string]string, len(stopRefs))
	if len(stopRefs) == 0 {
		return result
	}
	var stops []ctdf.Stop
	cursor, err := database.GetCollection("stops").Find(ctx, bson.M{"primaryidentifier": bson.M{"$in": stopRefs}})
	if err != nil {
		return result
	}
	if err := cursor.All(ctx, &stops); err != nil {
		_ = cursor.Close(ctx)
		return result
	}
	_ = cursor.Close(ctx)
	groupIDs := map[string]bool{}
	for _, stop := range stops {
		if stop.Timezone != "" {
			result[stop.PrimaryIdentifier] = stop.Timezone
		}
		for _, association := range stop.Associations {
			if association != nil && (association.Type == "" || association.Type == "stop_group") {
				addFareRef(groupIDs, association.AssociatedIdentifier)
			}
		}
	}
	if len(groupIDs) == 0 {
		return result
	}
	parentCursor, err := database.GetCollection("stops").Find(ctx, bson.M{"associations.associatedidentifier": bson.M{"$in": sortedFareRefs(groupIDs)}})
	if err != nil {
		return result
	}
	var parents []ctdf.Stop
	if err := parentCursor.All(ctx, &parents); err != nil {
		_ = parentCursor.Close(ctx)
		return result
	}
	_ = parentCursor.Close(ctx)
	groupTimezones := map[string]string{}
	groupHasStation := map[string]bool{}
	for _, parent := range parents {
		if parent.Timezone == "" {
			continue
		}
		station := parent.LocationType == "1" || strings.EqualFold(parent.LocationType, "station")
		for _, association := range parent.Associations {
			if association == nil || (association.Type != "" && association.Type != "stop_group") || !groupIDs[association.AssociatedIdentifier] {
				continue
			}
			group := association.AssociatedIdentifier
			if _, exists := groupTimezones[group]; !exists || (station && !groupHasStation[group]) {
				groupTimezones[group] = parent.Timezone
				groupHasStation[group] = station
			}
		}
	}
	for _, stop := range stops {
		if result[stop.PrimaryIdentifier] != "" {
			continue
		}
		for _, association := range stop.Associations {
			if association == nil || (association.Type != "" && association.Type != "stop_group") {
				continue
			}
			if timezone := groupTimezones[association.AssociatedIdentifier]; timezone != "" {
				result[stop.PrimaryIdentifier] = timezone
				break
			}
		}
	}
	return result
}

func readFareCollection(ctx context.Context, collection string, filter bson.M, output any) error {
	cursor, err := database.GetCollection(collection).Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	return cursor.All(ctx, output)
}

func fareLocationMatches(ref, stopRef, journeyLocationRef string, locations map[string]ctdf.FareLocation) bool {
	if ref == "" {
		return true
	}
	if journeyLocationRef == ref {
		return true
	}
	if journeyLocationRef != "" {
		if journeyLocation, exists := locations[journeyLocationRef]; exists && journeyLocation.ParentRef == ref {
			return true
		}
		if stageNumber := journeyFareStageNumber(journeyLocationRef); stageNumber != "" {
			for _, location := range locations {
				if location.Type == ctdf.FareLocationTypeStage && location.SourceIdentifier == stageNumber && location.ParentRef == ref {
					return true
				}
			}
		}
	}
	location, exists := locations[ref]
	if !exists {
		return false
	}
	for _, candidate := range location.StopRefs {
		if candidate == stopRef {
			return true
		}
	}
	return false
}

func fareRefSetIntersects(refs []string, candidates map[string]bool) bool {
	for _, ref := range refs {
		if candidates[ref] {
			return true
		}
	}
	return false
}

func fareLocationShouldSuppressFallback(refs map[string]bool, stopRef, journeyLocationRef string, locations map[string]ctdf.FareLocation) bool {
	for ref := range refs {
		location, exists := locations[ref]
		if exists && location.Type != ctdf.FareLocationTypeArea && fareLocationMatches(ref, stopRef, journeyLocationRef, locations) {
			return true
		}
	}
	matched := make([]string, 0)
	for ref, location := range locations {
		if location.Type == ctdf.FareLocationTypeArea && fareLocationMatches(ref, stopRef, journeyLocationRef, locations) {
			matched = append(matched, ref)
		}
	}
	if len(matched) == 0 {
		return false
	}
	for _, ref := range matched {
		if !refs[ref] {
			return false
		}
	}
	return true
}

func journeyItemsVisitFareLocations(items []ctdf.JourneyPlanRouteItem, refs []string, locations map[string]ctdf.FareLocation) bool {
	if len(refs) == 0 {
		return true
	}
	for _, ref := range refs {
		found := false
		for _, item := range items {
			if journeyItemVisitsFareLocations(item, []string{ref}, locations) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func journeyItemVisitsFareLocations(item ctdf.JourneyPlanRouteItem, refs []string, locations map[string]ctdf.FareLocation) bool {
	start, end, ok := fareItemLegRange(item)
	if !ok || len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		found := false
		for legIndex := start; legIndex < end; legIndex++ {
			origin, destination := item.Journey.LegEndpoints(legIndex)
			if origin == nil || destination == nil {
				continue
			}
			if fareLocationMatches(ref, origin.StopRef, "", locations) || fareLocationMatches(ref, destination.StopRef, "", locations) {
				found = true
				break
			}
			if origin.Outgoing != nil && (fareLocationMatches(ref, origin.StopRef, origin.Outgoing.OriginFareLocationRef, locations) ||
				fareLocationMatches(ref, destination.StopRef, origin.Outgoing.DestinationFareLocationRef, locations)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func journeyFareStageNumber(ref string) string {
	const marker = "-fare-location-stage-"
	index := strings.LastIndex(ref, marker)
	if index < 0 {
		return ""
	}
	return ref[index+len(marker):]
}

func fareItemLocationRef(item ctdf.JourneyPlanRouteItem, origin bool) string {
	_, _, start, end, ok := fareItemEndpointCalls(item)
	if !ok {
		return ""
	}
	if origin {
		boardingCall, _ := item.Journey.LegEndpoints(start)
		if boardingCall != nil && boardingCall.Outgoing != nil {
			return boardingCall.Outgoing.OriginFareLocationRef
		}
		return ""
	}
	previous, _ := item.Journey.LegEndpoints(end - 1)
	if previous != nil && previous.Outgoing != nil {
		return previous.Outgoing.DestinationFareLocationRef
	}
	return ""
}

func unavailableFare(reason string) *ctdf.FareQuote {
	return &ctdf.FareQuote{
		Certainty:    ctdf.FareCertaintyUnavailable,
		Availability: ctdf.FareAvailabilityUnknown,
		Reason:       reason,
	}
}

func appendFareReason(current, next string) string {
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	if strings.Contains(current, next) {
		return current
	}
	return current + "; " + next
}

func partialFare(reason string) *ctdf.FareQuote {
	return &ctdf.FareQuote{
		Certainty:    ctdf.FareCertaintyPartial,
		Availability: ctdf.FareAvailabilityUnknown,
		Reason:       reason,
	}
}
