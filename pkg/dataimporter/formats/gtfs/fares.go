package gtfs

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	fareversionlease "github.com/travigo/travigo/pkg/dataimporter/fareversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type gtfsFareAttribute struct {
	FareID           string `csv:"fare_id"`
	Price            string `csv:"price"`
	Currency         string `csv:"currency_type"`
	PaymentMethod    string `csv:"payment_method"`
	Transfers        string `csv:"transfers"`
	TransferDuration string `csv:"transfer_duration"`
	AgencyID         string `csv:"agency_id"`
}

type gtfsFareRule struct {
	FareID        string `csv:"fare_id"`
	RouteID       string `csv:"route_id"`
	OriginID      string `csv:"origin_id"`
	DestinationID string `csv:"destination_id"`
	ContainsID    string `csv:"contains_id"`
}

type gtfsFareRuleGroupKey struct {
	FareID        string
	RouteID       string
	OriginID      string
	DestinationID string
	HasContains   bool
}

type gtfsFareRuleGroup struct {
	row        gtfsFareRule
	contains   []string
	firstOrder int
}

func containsGTFSString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func gtfsFileHasColumn(filePath, column string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	header, err := csv.NewReader(bufio.NewReader(file)).Read()
	if err != nil {
		return false, err
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\uFEFF")
	}
	for _, field := range header {
		if strings.TrimSpace(field) == column {
			return true, nil
		}
	}
	return false, nil
}

func (g *Schedule) fareTimeframeAvailability(serviceID string) (*ctdf.Availability, []time.Time, []time.Time) {
	if serviceID == "" {
		return nil, nil, nil
	}
	availability := &ctdf.Availability{}
	var addedDates, removedDates []time.Time
	if calendar := g.calendarMapping[serviceID]; calendar != nil {
		for _, day := range calendar.GetRunningDays() {
			availability.Match = append(availability.Match, ctdf.AvailabilityRule{Type: ctdf.AvailabilityDayOfWeek, Value: day})
		}
		from, _ := time.Parse("20060102", calendar.Start)
		to, _ := time.Parse("20060102", calendar.End)
		availability.Condition = append(availability.Condition, ctdf.AvailabilityRule{
			Type:  ctdf.AvailabilityDateRange,
			Value: from.Format("2006-01-02") + ":" + to.Format("2006-01-02"),
		})
	}
	for _, calendarDate := range g.calendarDateMapping[serviceID] {
		date, err := time.Parse("20060102", calendarDate.Date)
		if err != nil {
			continue
		}
		rule := ctdf.AvailabilityRule{Type: ctdf.AvailabilityDate, Value: date.Format("2006-01-02")}
		switch calendarDate.ExceptionType {
		case 1:
			availability.Match = append(availability.Match, rule)
			addedDates = append(addedDates, date)
		case 2:
			availability.Exclude = append(availability.Exclude, rule)
			removedDates = append(removedDates, date)
		}
	}
	return availability, addedDates, removedDates
}

type gtfsFareProductV2 struct {
	ID              string `csv:"fare_product_id"`
	Name            string `csv:"fare_product_name"`
	Amount          string `csv:"amount"`
	Currency        string `csv:"currency"`
	RiderCategoryID string `csv:"rider_category_id"`
	FareMediaID     string `csv:"fare_media_id"`
}

type gtfsFareMediaV2 struct {
	ID   string `csv:"fare_media_id"`
	Name string `csv:"fare_media_name"`
	Type string `csv:"fare_media_type"`
}

type gtfsRiderCategoryV2 struct {
	ID             string `csv:"rider_category_id"`
	Name           string `csv:"rider_category_name"`
	IsDefault      string `csv:"is_default_fare_category"`
	EligibilityURL string `csv:"eligibility_url"`
}

type gtfsFareLegRuleV2 struct {
	LegGroupID           string `csv:"leg_group_id"`
	NetworkID            string `csv:"network_id"`
	FromAreaID           string `csv:"from_area_id"`
	ToAreaID             string `csv:"to_area_id"`
	FromTimeframeGroupID string `csv:"from_timeframe_group_id"`
	ToTimeframeGroupID   string `csv:"to_timeframe_group_id"`
	FareProductID        string `csv:"fare_product_id"`
	RulePriority         string `csv:"rule_priority"`
}

type gtfsTimeframeV2 struct {
	GroupID   string `csv:"timeframe_group_id"`
	StartTime string `csv:"start_time"`
	EndTime   string `csv:"end_time"`
	ServiceID string `csv:"service_id"`
}

type gtfsNetworkV2 struct {
	ID   string `csv:"network_id"`
	Name string `csv:"network_name"`
}

type gtfsRouteNetworkV2 struct {
	RouteID   string `csv:"route_id"`
	NetworkID string `csv:"network_id"`
}

type gtfsFareLegJoinRuleV2 struct {
	FromNetworkID string `csv:"from_network_id"`
	ToNetworkID   string `csv:"to_network_id"`
	FromStopID    string `csv:"from_stop_id"`
	ToStopID      string `csv:"to_stop_id"`
}

type gtfsFareTransferRuleV2 struct {
	FromLegGroupID    string `csv:"from_leg_group_id"`
	ToLegGroupID      string `csv:"to_leg_group_id"`
	TransferCount     string `csv:"transfer_count"`
	DurationLimit     string `csv:"duration_limit"`
	DurationLimitType string `csv:"duration_limit_type"`
	FareTransferType  string `csv:"fare_transfer_type"`
	FareProductID     string `csv:"fare_product_id"`
}

type gtfsStopArea struct {
	StopID string `csv:"stop_id"`
	AreaID string `csv:"area_id"`
}

type gtfsArea struct {
	ID   string `csv:"area_id"`
	Name string `csv:"area_name"`
}

type fareImportSummary struct {
	Present       bool
	Locations     int
	Products      int
	Offers        int
	Rules         int
	Tariffs       int
	Media         int
	Riders        int
	Networks      int
	RouteNetworks int
	Timeframes    int
	LegJoins      int
	Transfers     int
}

var gtfsCurrencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

func gtfsFareMoney(amount, currency string) (ctdf.FareMoney, error) {
	currency = strings.TrimSpace(currency)
	if !gtfsCurrencyCode.MatchString(currency) {
		return ctdf.FareMoney{}, fmt.Errorf("invalid ISO 4217 currency %q", currency)
	}
	exponent := uint8(2)
	switch currency {
	case "BIF", "CLP", "DJF", "GNF", "ISK", "JPY", "KMF", "KRW", "PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF":
		exponent = 0
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		exponent = 3
	case "CLF", "UYW":
		exponent = 4
	}
	value := strings.TrimSpace(amount)
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = strings.TrimPrefix(value, "-")
	}
	if value == "" {
		return ctdf.FareMoney{}, fmt.Errorf("fare amount %q must be a decimal", amount)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return ctdf.FareMoney{}, fmt.Errorf("invalid decimal fare amount %q", amount)
	}
	whole, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return ctdf.FareMoney{}, fmt.Errorf("invalid decimal fare amount %q", amount)
	}
	if currency == "CLF" || currency == "UYW" {
		exponent = 4
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > int(exponent) {
		return ctdf.FareMoney{}, fmt.Errorf("amount %q has more precision than %s supports", amount, currency)
	}
	for len(fraction) < int(exponent) {
		fraction += "0"
	}
	fractional := uint64(0)
	if fraction != "" {
		fractional, err = strconv.ParseUint(fraction, 10, 64)
		if err != nil {
			return ctdf.FareMoney{}, fmt.Errorf("invalid decimal fare amount %q", amount)
		}
	}
	multiplier := uint64(1)
	for index := uint8(0); index < exponent; index++ {
		multiplier *= 10
	}
	maxMagnitude := uint64(math.MaxInt64)
	if negative {
		maxMagnitude++
	}
	if whole > (maxMagnitude-fractional)/multiplier {
		return ctdf.FareMoney{}, fmt.Errorf("fare amount %q is too large", amount)
	}
	magnitude := whole*multiplier + fractional
	amountMinor := int64(magnitude)
	if negative {
		if magnitude == uint64(math.MaxInt64)+1 {
			amountMinor = -math.MaxInt64 - 1
		} else {
			amountMinor = -amountMinor
		}
	}
	money := ctdf.FareMoney{AmountMinor: amountMinor, Currency: currency, Exponent: exponent}
	return money, money.Validate()
}

func (g *Schedule) importFareData(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (fareImportSummary, error) {
	summary := fareImportSummary{}
	hasV2 := false
	for _, fileName := range []string{
		"fare_media.txt", "rider_categories.txt", "fare_products.txt", "timeframes.txt", "networks.txt",
		"route_networks.txt", "areas.txt", "stop_areas.txt", "fare_leg_rules.txt", "fare_leg_join_rules.txt", "fare_transfer_rules.txt",
	} {
		if _, exists := g.fileMap[fileName]; exists {
			hasV2 = true
			break
		}
	}
	_, hasV1Attributes := g.fileMap["fare_attributes.txt"]
	_, hasV1Rules := g.fileMap["fare_rules.txt"]
	if !hasV2 && !hasV1Attributes && !hasV1Rules {
		return summary, nil
	}
	summary.Present = true
	if hasV2 {
		if hasV1Attributes || hasV1Rules {
			log.Warn().Str("dataset", dataset.Identifier).Msg("GTFS feed contains Fares v1 and v2 tables; importing v2 and ignoring v1")
		}
		return g.importGTFSFaresV2(dataset, datasource, summary)
	}
	return g.importGTFSFaresV1(dataset, datasource, summary, hasV1Attributes, hasV1Rules)
}

func (g *Schedule) fareRecord(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, identifier, sourceID string) ctdf.FareRecord {
	return ctdf.FareRecord{
		PrimaryIdentifier: identifier,
		DatasetID:         dataset.Identifier,
		FareVersionID:     datasource.FareVersionID,
		SourceIdentifier:  sourceID,
		DataSource:        datasource,
		CreationDateTime:  time.Now(),
	}
}

func fareID(datasetID, kind, sourceID string) string {
	return fmt.Sprintf("%s-fare-%s-%s", datasetID, kind, sourceID)
}

func gtfsFareProductVariantID(datasetID, productID, riderCategoryID, mediaID string) string {
	return fareCompositeID(datasetID, "product-v2", productID, riderCategoryID, mediaID)
}

func fareCompositeID(datasetID, kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fareID(datasetID, kind, hex.EncodeToString(hash[:]))
}

func gtfsNetworkReference(datasetID, networkID string) string {
	if networkID == "" {
		return ""
	}
	return fmt.Sprintf("%s-network-%s", datasetID, networkID)
}

func (g *Schedule) fareURLForAgency(agencyID string) string {
	if agencyID != "" {
		for _, agency := range g.Agencies {
			if agency.ID == agencyID {
				return agency.FareURL
			}
		}
		return ""
	}
	if len(g.Agencies) == 1 {
		return g.Agencies[0].FareURL
	}
	return ""
}

func (g *Schedule) importGTFSFaresV1(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, summary fareImportSummary, hasAttributes, hasRules bool) (fareImportSummary, error) {
	if !hasAttributes {
		return summary, fmt.Errorf("GTFS fare_rules.txt is present without fare_attributes.txt")
	}
	tariffRef := fareID(dataset.Identifier, "tariff", "gtfs-v1")
	if err := upsertFareRecord("fare_tariffs", tariffRef, &ctdf.FareTariff{
		FareRecord: g.fareRecord(dataset, datasource, tariffRef, "gtfs-v1"),
		Name:       dataset.Provider.Name,
	}); err != nil {
		return fareImportSummary{}, err
	}
	summary.Tariffs = 1
	productIDs := map[string]string{}
	operatorRefs := map[string]string{}
	var parseErr error
	if err := importObject[gtfsFareAttribute](g, "fare_attributes.txt", "fare_products", true, func(row gtfsFareAttribute) (any, string) {
		if row.FareID == "" {
			parseErr = fmt.Errorf("GTFS fare_attributes.txt has an empty fare_id")
			return nil, ""
		}
		if _, exists := productIDs[row.FareID]; exists {
			parseErr = fmt.Errorf("GTFS fare_attributes.txt contains duplicate fare_id %q", row.FareID)
			return nil, ""
		}
		productRef := fareID(dataset.Identifier, "product", "v1-"+row.FareID)
		offerRef := fareID(dataset.Identifier, "offer", "v1-"+row.FareID)
		if row.PaymentMethod != "0" && row.PaymentMethod != "1" {
			parseErr = fmt.Errorf("GTFS fare %s has invalid payment_method %q", row.FareID, row.PaymentMethod)
			return nil, ""
		}
		money, err := gtfsFareMoney(row.Price, row.Currency)
		if err != nil {
			parseErr = fmt.Errorf("GTFS fare %s: %w", row.FareID, err)
			return nil, ""
		}
		if money.AmountMinor < 0 {
			parseErr = fmt.Errorf("GTFS fare %s has a negative fare_attributes.txt price", row.FareID)
			return nil, ""
		}
		var max *int
		if row.Transfers != "" {
			value, err := strconv.Atoi(row.Transfers)
			if err != nil || value < 0 || value > 2 {
				parseErr = fmt.Errorf("GTFS fare %s has invalid transfers value %q", row.FareID, row.Transfers)
				return nil, ""
			}
			max = &value
		}
		transfersUnlimited := strings.TrimSpace(row.Transfers) == ""
		var transferDuration *int64
		if row.TransferDuration != "" {
			value, err := strconv.ParseInt(row.TransferDuration, 10, 64)
			if err != nil || value < 0 {
				parseErr = fmt.Errorf("GTFS fare %s has invalid transfer_duration %q", row.FareID, row.TransferDuration)
				return nil, ""
			}
			transferDuration = &value
		}
		product := &ctdf.FareProduct{
			FareRecord:              g.fareRecord(dataset, datasource, productRef, row.FareID),
			Name:                    row.FareID,
			Type:                    ctdf.FareProductTypeSingleRide,
			PaymentMethod:           row.PaymentMethod,
			MaxTransfers:            max,
			TransfersUnlimited:      transfersUnlimited,
			TransferDurationSeconds: transferDuration,
		}
		productIDs[row.FareID] = productRef
		if row.AgencyID != "" {
			operatorRefs[row.FareID] = g.agencyOperatorRefs[row.AgencyID]
			if operatorRefs[row.FareID] == "" {
				parseErr = fmt.Errorf("GTFS fare %s references unknown agency_id %q", row.FareID, row.AgencyID)
				return nil, ""
			}
		}
		// A published static price describes the catalogue, not live ticket stock.
		offer := &ctdf.FareOffer{
			FareRecord:   g.fareRecord(dataset, datasource, offerRef, row.FareID),
			TariffRef:    tariffRef,
			ProductRef:   productRef,
			Price:        money,
			Availability: ctdf.FareAvailabilityUnknown,
			SalesURL:     g.fareURLForAgency(row.AgencyID),
		}
		// Product and offer use distinct collections; the catalogue price is
		// written alongside the product candidate.
		if err := upsertFareRecord("fare_offers", offerRef, offer); err != nil {
			parseErr = err
			return nil, ""
		}
		summary.Offers++
		summary.Products++
		return product, productRef
	}); err != nil {
		return fareImportSummary{}, fmt.Errorf("read GTFS fare_attributes.txt: %w", err)
	}
	if parseErr != nil {
		return fareImportSummary{}, parseErr
	}
	if len(productIDs) == 0 {
		return fareImportSummary{}, fmt.Errorf("GTFS fare_attributes.txt contains no fare records")
	}
	for zoneID, stopRefs := range g.fareStopRefs {
		locationRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeZone, zoneID)
		location := &ctdf.FareLocation{
			FareRecord: g.fareRecord(dataset, datasource, locationRef, zoneID),
			Name:       zoneID,
			Type:       ctdf.FareLocationTypeZone,
			StopRefs:   stopRefs,
		}
		if err := upsertFareRecord("fare_locations", locationRef, location); err != nil {
			return fareImportSummary{}, err
		}
		summary.Locations++
	}
	if hasRules {
		matchedFareIDs := map[string]bool{}
		ruleGroups := map[gtfsFareRuleGroupKey]*gtfsFareRuleGroup{}
		sequence := 0
		if err := importObject[gtfsFareRule](g, "fare_rules.txt", "fare_leg_rules", true, func(row gtfsFareRule) (any, string) {
			if _, exists := productIDs[row.FareID]; !exists {
				parseErr = fmt.Errorf("GTFS fare_rules.txt references unknown fare_id %q", row.FareID)
				return nil, ""
			}
			if row.RouteID != "" {
				if _, exists := g.routeMap[row.RouteID]; !exists {
					parseErr = fmt.Errorf("GTFS fare_rules.txt references unknown route_id %q", row.RouteID)
					return nil, ""
				}
			}
			for _, zoneID := range []string{row.OriginID, row.DestinationID, row.ContainsID} {
				if zoneID != "" && len(g.fareStopRefs[zoneID]) == 0 {
					parseErr = fmt.Errorf("GTFS fare_rules.txt references unknown zone_id %q", zoneID)
					return nil, ""
				}
			}
			sequence++
			matchedFareIDs[row.FareID] = true
			key := gtfsFareRuleGroupKey{
				FareID: row.FareID, RouteID: row.RouteID,
				OriginID: row.OriginID, DestinationID: row.DestinationID,
				HasContains: row.ContainsID != "",
			}
			group := ruleGroups[key]
			if group == nil {
				group = &gtfsFareRuleGroup{row: row, firstOrder: sequence}
				ruleGroups[key] = group
			}
			if row.ContainsID != "" && !containsGTFSString(group.contains, row.ContainsID) {
				group.contains = append(group.contains, row.ContainsID)
			}
			return nil, ""
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS fare_rules.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
		orderedGroups := make([]*gtfsFareRuleGroup, 0, len(ruleGroups))
		for _, group := range ruleGroups {
			orderedGroups = append(orderedGroups, group)
		}
		sort.Slice(orderedGroups, func(i, j int) bool { return orderedGroups[i].firstOrder < orderedGroups[j].firstOrder })
		for index, group := range orderedGroups {
			row := group.row
			ruleRef := fareID(dataset.Identifier, "rule-v1", fmt.Sprintf("%s-%d", row.FareID, index+1))
			rule := &ctdf.FareLegRule{
				FareRecord:   g.fareRecord(dataset, datasource, ruleRef, row.FareID),
				ProductRef:   productIDs[row.FareID],
				OfferRef:     fareID(dataset.Identifier, "offer", "v1-"+row.FareID),
				JourneyScope: true,
			}
			if row.RouteID != "" {
				rule.RouteRef = fmt.Sprintf("%s-service-%s", dataset.Identifier, row.RouteID)
			}
			if row.OriginID != "" {
				rule.OriginRef = ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeZone, row.OriginID)
			}
			if row.DestinationID != "" {
				rule.DestinationRef = ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeZone, row.DestinationID)
			}
			for _, zoneID := range group.contains {
				rule.ViaRefs = append(rule.ViaRefs, ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeZone, zoneID))
			}
			if operatorRef := operatorRefs[row.FareID]; operatorRef != "" {
				rule.OperatorRef = operatorRef
			}
			if err := upsertFareRecord("fare_leg_rules", ruleRef, rule); err != nil {
				return fareImportSummary{}, err
			}
			summary.Rules++
		}
		// A fare with no rule rows is a feed-wide flat fare.
		for fareIDValue, productRef := range productIDs {
			if matchedFareIDs[fareIDValue] {
				continue
			}
			if err := g.insertV1DefaultFareRule(dataset, datasource, fareIDValue, productRef, operatorRefs[fareIDValue], sequence); err != nil {
				return fareImportSummary{}, err
			}
			sequence++
			summary.Rules++
		}
	} else {
		for fareIDValue, productRef := range productIDs {
			if err := g.insertV1DefaultFareRule(dataset, datasource, fareIDValue, productRef, operatorRefs[fareIDValue], 0); err != nil {
				return fareImportSummary{}, err
			}
			summary.Rules++
		}
	}
	return summary, nil
}

func (g *Schedule) insertV1DefaultFareRule(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, sourceID, productRef, operatorRef string, sequence int) error {
	ruleRef := fareID(dataset.Identifier, "rule-v1-flat", fmt.Sprintf("%s-%d", sourceID, sequence))
	rule := &ctdf.FareLegRule{
		FareRecord:   g.fareRecord(dataset, datasource, ruleRef, sourceID),
		ProductRef:   productRef,
		OfferRef:     fareID(dataset.Identifier, "offer", "v1-"+sourceID),
		OperatorRef:  operatorRef,
		JourneyScope: true,
	}
	return upsertFareRecord("fare_leg_rules", ruleRef, rule)
}

func (g *Schedule) importGTFSFaresV2(dataset datasets.DataSet, datasource *ctdf.DataSourceReference, summary fareImportSummary) (fareImportSummary, error) {
	if _, exists := g.fileMap["fare_products.txt"]; !exists {
		return fareImportSummary{}, fmt.Errorf("GTFS Fares v2 rules are present without fare_products.txt")
	}
	if _, exists := g.fileMap["fare_leg_rules.txt"]; !exists {
		if _, hasJoins := g.fileMap["fare_leg_join_rules.txt"]; hasJoins {
			return fareImportSummary{}, fmt.Errorf("GTFS fare_leg_join_rules.txt is present without fare_leg_rules.txt")
		}
		if _, hasTransfers := g.fileMap["fare_transfer_rules.txt"]; hasTransfers {
			return fareImportSummary{}, fmt.Errorf("GTFS fare_transfer_rules.txt is present without fare_leg_rules.txt")
		}
	}
	tariffRef := fareID(dataset.Identifier, "tariff", "gtfs-v2")
	if err := upsertFareRecord("fare_tariffs", tariffRef, &ctdf.FareTariff{
		FareRecord: g.fareRecord(dataset, datasource, tariffRef, "gtfs-v2"),
		Name:       dataset.Provider.Name,
	}); err != nil {
		return fareImportSummary{}, err
	}
	summary.Tariffs = 1
	var parseErr error
	riderRefs := map[string]string{}
	mediaRefs := map[string]string{}
	if _, exists := g.fileMap["rider_categories.txt"]; exists {
		if err := importObject[gtfsRiderCategoryV2](g, "rider_categories.txt", "fare_rider_categories", true, func(row gtfsRiderCategoryV2) (any, string) {
			if row.ID == "" {
				parseErr = fmt.Errorf("GTFS rider_categories.txt has an empty rider_category_id")
				return nil, ""
			}
			if _, exists := riderRefs[row.ID]; exists {
				parseErr = fmt.Errorf("GTFS rider_categories.txt contains duplicate rider_category_id %q", row.ID)
				return nil, ""
			}
			isDefault := false
			switch row.IsDefault {
			case "", "0":
			case "1":
				isDefault = true
			default:
				parseErr = fmt.Errorf("GTFS rider category %s has invalid is_default_fare_category %q", row.ID, row.IsDefault)
				return nil, ""
			}
			identifier := fareID(dataset.Identifier, "rider", row.ID)
			riderRefs[row.ID] = identifier
			summary.Riders++
			return &ctdf.FareRiderCategory{
				FareRecord:     g.fareRecord(dataset, datasource, identifier, row.ID),
				Name:           row.Name,
				IsDefault:      isDefault,
				EligibilityURL: row.EligibilityURL,
			}, identifier
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS rider_categories.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}
	if _, exists := g.fileMap["fare_media.txt"]; exists {
		if err := importObject[gtfsFareMediaV2](g, "fare_media.txt", "fare_media", true, func(row gtfsFareMediaV2) (any, string) {
			if row.ID == "" {
				parseErr = fmt.Errorf("GTFS fare_media.txt has an empty fare_media_id")
				return nil, ""
			}
			if _, exists := mediaRefs[row.ID]; exists {
				parseErr = fmt.Errorf("GTFS fare_media.txt contains duplicate fare_media_id %q", row.ID)
				return nil, ""
			}
			identifier := fareID(dataset.Identifier, "media", row.ID)
			mediaRefs[row.ID] = identifier
			mediaType := ctdf.FareMediaTypeOther
			switch row.Type {
			case "0":
				mediaType = ctdf.FareMediaTypeNone
			case "1":
				mediaType = ctdf.FareMediaTypePaper
			case "2":
				mediaType = ctdf.FareMediaTypeTransitCard
			case "3":
				mediaType = ctdf.FareMediaTypeContactless
			case "4":
				mediaType = ctdf.FareMediaTypeApp
			default:
				parseErr = fmt.Errorf("GTFS fare media %s has invalid fare_media_type %q", row.ID, row.Type)
				return nil, ""
			}
			summary.Media++
			return &ctdf.FareMedia{
				FareRecord: g.fareRecord(dataset, datasource, identifier, row.ID),
				Name:       row.Name,
				Type:       mediaType,
			}, identifier
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS fare_media.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}
	productRefs := map[string][]string{}
	offerRefs := map[string][]string{}
	seenProductVariants := map[string]bool{}
	if err := importObject[gtfsFareProductV2](g, "fare_products.txt", "fare_products", true, func(row gtfsFareProductV2) (any, string) {
		if row.ID == "" {
			parseErr = fmt.Errorf("GTFS fare_products.txt has an empty fare_product_id")
			return nil, ""
		}
		variantKey := row.ID + "\x00" + row.RiderCategoryID + "\x00" + row.FareMediaID
		if seenProductVariants[variantKey] {
			parseErr = fmt.Errorf("GTFS fare_products.txt contains duplicate product/rider/media key %q", row.ID)
			return nil, ""
		}
		seenProductVariants[variantKey] = true
		productRef := gtfsFareProductVariantID(dataset.Identifier, row.ID, row.RiderCategoryID, row.FareMediaID)
		offerRef := fareID(dataset.Identifier, "offer-v2", strings.TrimPrefix(productRef, dataset.Identifier+"-fare-product-v2-"))
		money, err := gtfsFareMoney(row.Amount, row.Currency)
		if err != nil {
			parseErr = fmt.Errorf("GTFS fare product %s: %w", row.ID, err)
			return nil, ""
		}
		product := &ctdf.FareProduct{
			FareRecord:      g.fareRecord(dataset, datasource, productRef, row.ID+"/"+row.RiderCategoryID+"/"+row.FareMediaID),
			ProductGroupRef: fareID(dataset.Identifier, "product-group", "v2-"+row.ID),
			Name:            row.Name,
			Type:            ctdf.FareProductTypeSingleRide,
		}
		if product.Name == "" {
			product.Name = row.ID
		}
		if row.RiderCategoryID != "" {
			var exists bool
			product.RiderRef, exists = riderRefs[row.RiderCategoryID]
			if !exists {
				parseErr = fmt.Errorf("GTFS fare_products.txt references unknown rider_category_id %q", row.RiderCategoryID)
				return nil, ""
			}
		}
		if row.FareMediaID != "" {
			var exists bool
			product.MediaRef, exists = mediaRefs[row.FareMediaID]
			if !exists {
				parseErr = fmt.Errorf("GTFS fare_products.txt references unknown fare_media_id %q", row.FareMediaID)
				return nil, ""
			}
		}
		offer := &ctdf.FareOffer{
			FareRecord:   g.fareRecord(dataset, datasource, offerRef, row.ID),
			TariffRef:    tariffRef,
			ProductRef:   productRef,
			Price:        money,
			Availability: ctdf.FareAvailabilityUnknown,
			SalesURL:     g.fareURLForAgency(""),
		}
		if err := upsertFareRecord("fare_offers", offerRef, offer); err != nil {
			parseErr = err
			return nil, ""
		}
		productRefs[row.ID] = append(productRefs[row.ID], productRef)
		offerRefs[row.ID] = append(offerRefs[row.ID], offerRef)
		summary.Products++
		summary.Offers++
		return product, productRef
	}); err != nil {
		return fareImportSummary{}, fmt.Errorf("read GTFS fare_products.txt: %w", err)
	}
	if parseErr != nil {
		return fareImportSummary{}, parseErr
	}
	if len(productRefs) == 0 {
		return fareImportSummary{}, fmt.Errorf("GTFS fare_products.txt contains no fare products")
	}
	areaStops := map[string][]string{}
	stopHasExplicitArea := map[string]bool{}
	areaNames := map[string]string{}
	if _, exists := g.fileMap["areas.txt"]; exists {
		seenAreaIDs := map[string]bool{}
		if err := importObject[gtfsArea](g, "areas.txt", "fare_locations", false, func(row gtfsArea) (any, string) {
			if row.ID == "" {
				parseErr = fmt.Errorf("GTFS areas.txt has an empty area_id")
				return nil, ""
			}
			if seenAreaIDs[row.ID] {
				parseErr = fmt.Errorf("GTFS areas.txt contains duplicate area_id %q", row.ID)
				return nil, ""
			}
			seenAreaIDs[row.ID] = true
			areaNames[row.ID] = row.Name
			return nil, ""
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS areas.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}
	if _, exists := g.fileMap["stop_areas.txt"]; exists {
		if err := importObject[gtfsStopArea](g, "stop_areas.txt", "fare_locations", false, func(row gtfsStopArea) (any, string) {
			if _, exists := areaNames[row.AreaID]; row.AreaID == "" || !exists {
				parseErr = fmt.Errorf("GTFS stop_areas.txt references unknown area_id %q", row.AreaID)
				return nil, ""
			}
			if _, exists := g.stopNames[row.StopID]; row.StopID == "" || !exists {
				parseErr = fmt.Errorf("GTFS stop_areas.txt references unknown stop_id %q", row.StopID)
				return nil, ""
			}
			areaStops[row.AreaID] = append(areaStops[row.AreaID], gtfsStopReference(dataset.Identifier, row.StopID))
			stopHasExplicitArea[row.StopID] = true
			return nil, ""
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS stop_areas.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}
	// Platforms inherit their parent station's area only when the platform has
	// no explicit stop_areas assignment of its own.
	for areaID, stops := range areaStops {
		for _, stationRef := range stops {
			const stopPrefix = "-stop-"
			separator := strings.LastIndex(stationRef, stopPrefix)
			if separator < 0 {
				continue
			}
			stationID := stationRef[separator+len(stopPrefix):]
			for _, childStopID := range g.stopChildren[stationID] {
				if stopHasExplicitArea[childStopID] {
					continue
				}
				childRef := gtfsStopReference(dataset.Identifier, childStopID)
				if !containsGTFSString(areaStops[areaID], childRef) {
					areaStops[areaID] = append(areaStops[areaID], childRef)
				}
			}
		}
	}
	for areaID, name := range areaNames {
		locationRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeArea, areaID)
		location := &ctdf.FareLocation{
			FareRecord: g.fareRecord(dataset, datasource, locationRef, areaID),
			Name:       name,
			Type:       ctdf.FareLocationTypeArea,
			StopRefs:   areaStops[areaID],
		}
		if err := upsertFareRecord("fare_locations", locationRef, location); err != nil {
			return fareImportSummary{}, err
		}
		summary.Locations++
	}

	knownNetworks := map[string]string{}
	if _, exists := g.fileMap["networks.txt"]; exists {
		for _, route := range g.routeMap {
			if route.NetworkID != "" {
				return fareImportSummary{}, fmt.Errorf("GTFS networks.txt is forbidden when routes.txt contains network_id")
			}
		}
		if err := importObject[gtfsNetworkV2](g, "networks.txt", "fare_networks", true, func(row gtfsNetworkV2) (any, string) {
			if row.ID == "" {
				parseErr = fmt.Errorf("GTFS networks.txt has an empty network_id")
				return nil, ""
			}
			if _, exists := knownNetworks[row.ID]; exists {
				parseErr = fmt.Errorf("GTFS networks.txt contains duplicate network_id %q", row.ID)
				return nil, ""
			}
			identifier := gtfsNetworkReference(dataset.Identifier, row.ID)
			knownNetworks[row.ID] = identifier
			summary.Networks++
			return &ctdf.FareNetwork{
				FareRecord: g.fareRecord(dataset, datasource, identifier, row.ID),
				Name:       row.Name,
			}, identifier
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS networks.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}
	// GTFS permits network IDs to be supplied directly in routes.txt. Capture
	// those references too, even when there is no networks.txt name catalogue.
	for _, route := range g.routeMap {
		if route.NetworkID == "" {
			continue
		}
		if _, exists := knownNetworks[route.NetworkID]; !exists {
			networkRef := gtfsNetworkReference(dataset.Identifier, route.NetworkID)
			knownNetworks[route.NetworkID] = networkRef
			if err := upsertFareRecord("fare_networks", networkRef, &ctdf.FareNetwork{
				FareRecord: g.fareRecord(dataset, datasource, networkRef, route.NetworkID),
			}); err != nil {
				return fareImportSummary{}, err
			}
			summary.Networks++
		}
		// Store route-to-network links in the fare version as well as the
		// schedule service record, so planning still works when service objects
		// are intentionally omitted from this dataset's imported objects.
		routeNetworkID := fareCompositeID(dataset.Identifier, "route-network", route.ID, route.NetworkID)
		serviceRef := fmt.Sprintf("%s-service-%s", dataset.Identifier, route.ID)
		if err := upsertFareRecord("fare_route_networks", routeNetworkID, &ctdf.FareRouteNetwork{
			FareRecord: g.fareRecord(dataset, datasource, routeNetworkID, route.ID+"/"+route.NetworkID),
			RouteRef:   serviceRef,
			NetworkRef: knownNetworks[route.NetworkID],
		}); err != nil {
			return fareImportSummary{}, err
		}
		summary.RouteNetworks++
	}
	if _, exists := g.fileMap["route_networks.txt"]; exists {
		for _, route := range g.routeMap {
			if route.NetworkID != "" {
				return fareImportSummary{}, fmt.Errorf("GTFS route_networks.txt is forbidden when routes.txt contains network_id")
			}
		}
		seenRouteNetwork := map[string]bool{}
		if err := importObject[gtfsRouteNetworkV2](g, "route_networks.txt", "fare_route_networks", true, func(row gtfsRouteNetworkV2) (any, string) {
			if _, exists := g.routeMap[row.RouteID]; row.RouteID == "" || !exists {
				parseErr = fmt.Errorf("GTFS route_networks.txt references unknown route_id %q", row.RouteID)
				return nil, ""
			}
			networkRef, exists := knownNetworks[row.NetworkID]
			if row.NetworkID == "" || !exists {
				parseErr = fmt.Errorf("GTFS route_networks.txt references unknown network_id %q", row.NetworkID)
				return nil, ""
			}
			if seenRouteNetwork[row.RouteID] {
				parseErr = fmt.Errorf("GTFS route_networks.txt assigns route_id %q more than once", row.RouteID)
				return nil, ""
			}
			seenRouteNetwork[row.RouteID] = true
			identifier := fareCompositeID(dataset.Identifier, "route-network", row.RouteID, row.NetworkID)
			serviceRef := fmt.Sprintf("%s-service-%s", dataset.Identifier, row.RouteID)
			summary.RouteNetworks++
			return &ctdf.FareRouteNetwork{
				FareRecord: g.fareRecord(dataset, datasource, identifier, row.RouteID+"/"+row.NetworkID),
				RouteRef:   serviceRef,
				NetworkRef: networkRef,
			}, identifier
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS route_networks.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}

	timeframeRefs := map[string]string{}
	if _, exists := g.fileMap["timeframes.txt"]; exists {
		sequence := 0
		if err := importObject[gtfsTimeframeV2](g, "timeframes.txt", "fare_timeframes", true, func(row gtfsTimeframeV2) (any, string) {
			if row.GroupID == "" {
				parseErr = fmt.Errorf("GTFS timeframes.txt has an empty timeframe_group_id")
				return nil, ""
			}
			var err error
			start := time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)
			if row.StartTime != "" {
				start, err = parseGTFSTime(row.StartTime)
			}
			if err != nil {
				parseErr = fmt.Errorf("GTFS timeframe %s has invalid start_time: %w", row.GroupID, err)
				return nil, ""
			}
			if gtfsTimeSeconds(start) > 24*60*60 {
				parseErr = fmt.Errorf("GTFS timeframe %s has invalid start_time %q", row.GroupID, row.StartTime)
				return nil, ""
			}
			end := time.Date(0, time.January, 2, 0, 0, 0, 0, time.UTC)
			if row.EndTime != "" {
				end, err = parseGTFSTime(row.EndTime)
			}
			if err != nil {
				parseErr = fmt.Errorf("GTFS timeframe %s has invalid end_time: %w", row.GroupID, err)
				return nil, ""
			}
			if gtfsTimeSeconds(end) > 24*60*60 || !start.Before(end) {
				parseErr = fmt.Errorf("GTFS timeframe %s has invalid end_time %q", row.GroupID, row.EndTime)
				return nil, ""
			}
			serviceRef := ""
			if row.ServiceID == "" {
				parseErr = fmt.Errorf("GTFS timeframes.txt has an empty service_id")
				return nil, ""
			}
			_, hasCalendar := g.calendarMapping[row.ServiceID]
			_, hasCalendarDate := g.calendarDateMapping[row.ServiceID]
			if !hasCalendar && !hasCalendarDate {
				parseErr = fmt.Errorf("GTFS timeframes.txt references unknown service_id %q", row.ServiceID)
				return nil, ""
			}
			serviceRef = fmt.Sprintf("%s-calendar-service-%s", dataset.Identifier, row.ServiceID)
			sequence++
			groupRef := fareID(dataset.Identifier, "timeframe-group", row.GroupID)
			identifier := fareID(dataset.Identifier, "timeframe-row", fmt.Sprintf("%s-%d", row.GroupID, sequence))
			timeframeRefs[row.GroupID] = groupRef
			summary.Timeframes++
			availability, addedDates, removedDates := g.fareTimeframeAvailability(row.ServiceID)
			return &ctdf.FareTimeframe{
				FareRecord:      g.fareRecord(dataset, datasource, identifier, row.GroupID),
				GroupRef:        groupRef,
				StartTimeSecond: gtfsTimeSeconds(start),
				EndTimeSecond:   gtfsTimeSeconds(end),
				ServiceRef:      serviceRef,
				Availability:    availability,
				AddedDates:      addedDates,
				RemovedDates:    removedDates,
			}, identifier
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS timeframes.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
	}

	if _, exists := g.fileMap["fare_leg_rules.txt"]; exists {
		hasRulePriority, err := gtfsFileHasColumn(g.fileMap["fare_leg_rules.txt"], "rule_priority")
		if err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS fare_leg_rules.txt header: %w", err)
		}
		sequence := 0
		legGroupRefs := map[string]string{}
		if err := importObject[gtfsFareLegRuleV2](g, "fare_leg_rules.txt", "fare_leg_rules", true, func(row gtfsFareLegRuleV2) (any, string) {
			productVariants := productRefs[row.FareProductID]
			productOfferVariants := offerRefs[row.FareProductID]
			if len(productVariants) == 0 || len(productOfferVariants) != len(productVariants) {
				parseErr = fmt.Errorf("GTFS fare_leg_rules.txt references unknown fare_product_id %q", row.FareProductID)
				return nil, ""
			}
			for _, areaID := range []string{row.FromAreaID, row.ToAreaID} {
				if areaID != "" {
					if _, exists := areaNames[areaID]; !exists {
						parseErr = fmt.Errorf("GTFS fare_leg_rules.txt references unknown area_id %q", areaID)
						return nil, ""
					}
				}
			}
			if row.NetworkID != "" {
				if _, exists := knownNetworks[row.NetworkID]; !exists {
					parseErr = fmt.Errorf("GTFS fare_leg_rules.txt references unknown network_id %q", row.NetworkID)
					return nil, ""
				}
			}
			for _, groupID := range []string{row.FromTimeframeGroupID, row.ToTimeframeGroupID} {
				if groupID != "" {
					if _, exists := timeframeRefs[groupID]; !exists {
						parseErr = fmt.Errorf("GTFS fare_leg_rules.txt references unknown timeframe_group_id %q", groupID)
						return nil, ""
					}
				}
			}
			priority := 0
			if row.RulePriority != "" {
				priorityValue, priorityErr := strconv.Atoi(row.RulePriority)
				if priorityErr != nil || priorityValue < 0 {
					parseErr = fmt.Errorf("GTFS fare_leg_rules.txt has invalid rule_priority %q", row.RulePriority)
					return nil, ""
				}
				priority = priorityValue
			}
			sequence++
			ruleRef := fareID(dataset.Identifier, "rule-v2", fmt.Sprintf("%s-%d", row.FareProductID, sequence))
			legGroupRef := ""
			if row.LegGroupID != "" {
				legGroupRef = fareID(dataset.Identifier, "leg-group", row.LegGroupID)
				legGroupRefs[row.LegGroupID] = legGroupRef
			}
			rule := &ctdf.FareLegRule{
				FareRecord:             g.fareRecord(dataset, datasource, ruleRef, row.LegGroupID),
				LegGroupRef:            legGroupRef,
				RulePriority:           priority,
				EmptyFieldsAreFallback: !hasRulePriority,
				ProductRef:             productVariants[0],
				OfferRef:               productOfferVariants[0],
				ProductRefs:            productVariants,
				OfferRefs:              productOfferVariants,
			}
			if row.FromAreaID != "" {
				rule.OriginRef = ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeArea, row.FromAreaID)
			}
			if row.ToAreaID != "" {
				rule.DestinationRef = ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeArea, row.ToAreaID)
			}
			if row.NetworkID != "" {
				rule.NetworkRef = gtfsNetworkReference(dataset.Identifier, row.NetworkID)
			}
			rule.FromTimeframeRef = timeframeRefs[row.FromTimeframeGroupID]
			rule.ToTimeframeRef = timeframeRefs[row.ToTimeframeGroupID]
			summary.Rules++
			return rule, ruleRef
		}); err != nil {
			return fareImportSummary{}, fmt.Errorf("read GTFS fare_leg_rules.txt: %w", err)
		}
		if parseErr != nil {
			return fareImportSummary{}, parseErr
		}
		if _, exists := g.fileMap["fare_leg_join_rules.txt"]; exists {
			sequence := 0
			if err := importObject[gtfsFareLegJoinRuleV2](g, "fare_leg_join_rules.txt", "fare_effective_leg_joins", true, func(row gtfsFareLegJoinRuleV2) (any, string) {
				if (row.FromNetworkID == "") != (row.ToNetworkID == "") {
					parseErr = fmt.Errorf("GTFS fare_leg_join_rules.txt requires from_network_id and to_network_id together")
					return nil, ""
				}
				if (row.FromStopID == "") != (row.ToStopID == "") {
					parseErr = fmt.Errorf("GTFS fare_leg_join_rules.txt requires from_stop_id and to_stop_id together")
					return nil, ""
				}
				for _, networkID := range []string{row.FromNetworkID, row.ToNetworkID} {
					if networkID != "" {
						if _, exists := knownNetworks[networkID]; !exists {
							parseErr = fmt.Errorf("GTFS fare_leg_join_rules.txt references unknown network_id %q", networkID)
							return nil, ""
						}
					}
				}
				for _, stopID := range []string{row.FromStopID, row.ToStopID} {
					if stopID != "" {
						if _, exists := g.stopNames[stopID]; !exists {
							parseErr = fmt.Errorf("GTFS fare_leg_join_rules.txt references unknown stop_id %q", stopID)
							return nil, ""
						}
					}
				}
				sequence++
				identifier := fareID(dataset.Identifier, "leg-join", fmt.Sprintf("%d", sequence))
				join := &ctdf.FareEffectiveLegJoin{FareRecord: g.fareRecord(dataset, datasource, identifier, fmt.Sprintf("row-%d", sequence))}
				if row.FromNetworkID != "" {
					join.FromNetworkRef = knownNetworks[row.FromNetworkID]
				}
				if row.ToNetworkID != "" {
					join.ToNetworkRef = knownNetworks[row.ToNetworkID]
				}
				if row.FromStopID != "" {
					join.FromStopRef = gtfsStopReference(dataset.Identifier, row.FromStopID)
				}
				if row.ToStopID != "" {
					join.ToStopRef = gtfsStopReference(dataset.Identifier, row.ToStopID)
				}
				summary.LegJoins++
				return join, identifier
			}); err != nil {
				return fareImportSummary{}, fmt.Errorf("read GTFS fare_leg_join_rules.txt: %w", err)
			}
			if parseErr != nil {
				return fareImportSummary{}, parseErr
			}
		}
		if _, exists := g.fileMap["fare_transfer_rules.txt"]; exists {
			sequence := 0
			if err := importObject[gtfsFareTransferRuleV2](g, "fare_transfer_rules.txt", "fare_transfer_rules", true, func(row gtfsFareTransferRuleV2) (any, string) {
				fromGroup := ""
				toGroup := ""
				if row.FromLegGroupID != "" {
					var exists bool
					fromGroup, exists = legGroupRefs[row.FromLegGroupID]
					if !exists {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt references unknown from_leg_group_id %q", row.FromLegGroupID)
						return nil, ""
					}
				}
				if row.ToLegGroupID != "" {
					var exists bool
					toGroup, exists = legGroupRefs[row.ToLegGroupID]
					if !exists {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt references unknown to_leg_group_id %q", row.ToLegGroupID)
						return nil, ""
					}
				}
				if row.FareTransferType == "" {
					parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt has an empty fare_transfer_type")
					return nil, ""
				}
				transferType, err := strconv.Atoi(row.FareTransferType)
				if err != nil || transferType < 0 || transferType > 2 {
					parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt has invalid fare_transfer_type %q", row.FareTransferType)
					return nil, ""
				}
				var transferCount *int
				if row.TransferCount != "" {
					value, err := strconv.Atoi(row.TransferCount)
					if err != nil || value == 0 || value < -1 {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt has invalid transfer_count %q", row.TransferCount)
						return nil, ""
					}
					transferCount = &value
				}
				if row.FromLegGroupID == row.ToLegGroupID && transferCount == nil {
					parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt requires transfer_count for identical leg groups")
					return nil, ""
				}
				if row.FromLegGroupID != row.ToLegGroupID && transferCount != nil {
					parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt forbids transfer_count for different leg groups")
					return nil, ""
				}
				var duration *int
				if row.DurationLimit != "" {
					value, err := strconv.Atoi(row.DurationLimit)
					if err != nil || value <= 0 {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt has invalid duration_limit %q", row.DurationLimit)
						return nil, ""
					}
					duration = &value
					limitType, err := strconv.Atoi(row.DurationLimitType)
					if err != nil || limitType < 0 || limitType > 3 {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt has invalid duration_limit_type %q", row.DurationLimitType)
						return nil, ""
					}
				} else if row.DurationLimitType != "" {
					parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt duration_limit_type requires duration_limit")
					return nil, ""
				}
				productRef := ""
				var productVariants []string
				if row.FareProductID != "" {
					productVariants = productRefs[row.FareProductID]
					if len(productVariants) == 0 {
						parseErr = fmt.Errorf("GTFS fare_transfer_rules.txt references unknown fare_product_id %q", row.FareProductID)
						return nil, ""
					}
					productRef = productVariants[0]
				}
				sequence++
				identifier := fareID(dataset.Identifier, "transfer-rule", fmt.Sprintf("%d", sequence))
				summary.Transfers++
				return &ctdf.FareTransferRule{
					FareRecord:      g.fareRecord(dataset, datasource, identifier, fmt.Sprintf("row-%d", sequence)),
					FromLegGroupRef: fromGroup,
					ToLegGroupRef:   toGroup,
					ProductRef:      productRef,
					ProductRefs:     productVariants,
					TransferCount:   transferCount,
					DurationSeconds: duration,
					DurationType:    row.DurationLimitType,
					TransferType:    row.FareTransferType,
				}, identifier
			}); err != nil {
				return fareImportSummary{}, fmt.Errorf("read GTFS fare_transfer_rules.txt: %w", err)
			}
			if parseErr != nil {
				return fareImportSummary{}, parseErr
			}
		}
	}
	return summary, nil
}

func upsertFareRecord(collectionName, identifier string, value any) error {
	versioned, ok := value.(interface {
		GetFareDatasetID() string
		GetFareVersionID() string
	})
	if !ok || versioned.GetFareDatasetID() == "" || versioned.GetFareVersionID() == "" {
		return fmt.Errorf("fare record %s has no dataset or version identity", identifier)
	}
	ctx := context.Background()
	var datasource *ctdf.DataSourceReference
	if withRecord, ok := value.(interface{ GetFareRecord() ctdf.FareRecord }); ok {
		if record := withRecord.GetFareRecord(); record.DataSource != nil {
			datasource = record.DataSource
			ctx = datasource.GetImportContext()
		}
	}
	if err := fareversionlease.EnsureImportLease(ctx, datasource); err != nil {
		return err
	}
	filter := bson.M{
		"primaryidentifier": identifier,
		"datasetid":         versioned.GetFareDatasetID(),
		"fareversionid":     versioned.GetFareVersionID(),
	}
	_, err := database.GetCollection(collectionName).ReplaceOne(ctx, filter, value, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("store fare record %s: %w", identifier, err)
	}
	return nil
}
