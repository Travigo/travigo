package netex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	fareversionlease "github.com/travigo/travigo/pkg/dataimporter/fareversion"
	"go.mongodb.org/mongo-driver/bson"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/net/html/charset"
)

// Fares imports the BODS NeTEx fare profile into the shared CTDF fare model.
// It deliberately imports catalogue data only: ticket stock and availability
// are not represented by a published NeTEx price.
type Fares struct {
	root            *xmlNode
	SourceNamespace string
	SourceOperators []string
}

type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []*xmlNode
}

type netexDistanceMatrixElement struct {
	from          string
	to            string
	priceGroupIDs []string
}

type netexFareTablePrice struct {
	matrixID       string
	priceID        string
	originRef      string
	destinationRef string
}

func (node *xmlNode) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	node.Name = start.Name
	node.Attrs = append(node.Attrs[:0], start.Attr...)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			child := &xmlNode{}
			if err := child.UnmarshalXML(decoder, value); err != nil {
				return err
			}
			node.Children = append(node.Children, child)
		case xml.CharData:
			node.Text += string(value)
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

func (format *Fares) ParseFile(reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = charset.NewReaderLabel
	var root xmlNode
	if err := decoder.Decode(&root); err != nil {
		return fmt.Errorf("parse NeTEx XML: %w", err)
	}
	if root.Name.Local == "" {
		return fmt.Errorf("NeTEx document has no root element")
	}
	switch root.Name.Local {
	case "PublicationDelivery", "CompositeFrame", "FareFrame", "ServiceFrame", "ResourceFrame":
	default:
		if len(findNodes(&root, "FareFrame")) == 0 {
			return fmt.Errorf("XML document does not contain NeTEx fare data")
		}
	}
	if format.root == nil {
		format.root = &xmlNode{Name: xml.Name{Local: "NeTExDocuments"}}
	}
	format.root.Children = append(format.root.Children, &root)
	return nil
}

func (format *Fares) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) (datasets.DataImportReport, error) {
	if format.root == nil {
		return datasets.DataImportReport{}, fmt.Errorf("NeTEx document has not been parsed")
	}
	if datasource == nil || datasource.FareVersionID == "" {
		return datasets.DataImportReport{}, fmt.Errorf("NeTEx fare import requires a candidate fare version")
	}
	if len(findNodes(format.root, "FareFrame")) == 0 {
		return datasets.DataImportReport{}, fmt.Errorf("NeTEx document set contains no FareFrame")
	}
	datasource.OriginalFormat = string(datasets.DataSetFormatNetExFares)
	baseRecord := func(kind, sourceID string) ctdf.FareRecord {
		scopedSourceID := format.scopeSourceID(sourceID)
		identifier := netexID(dataset.Identifier, kind, scopedSourceID)
		return ctdf.FareRecord{
			PrimaryIdentifier: identifier,
			DatasetID:         dataset.Identifier,
			FareVersionID:     datasource.FareVersionID,
			SourceIdentifier:  scopedSourceID,
			DataSource:        datasource,
			CreationDateTime:  time.Now(),
		}
	}
	report := datasets.DataImportReport{FareDataPresent: true}
	frames := findNodes(format.root, "FareFrame")
	serviceFrames := findNodes(format.root, "ServiceFrame")
	operatorGroups := map[string][]string{}
	for _, resourceFrame := range findNodes(format.root, "ResourceFrame") {
		for _, group := range findNodes(resourceFrame, "GroupOfOperators") {
			id := nodeAttr(group, "id")
			if id == "" {
				continue
			}
			for _, ref := range findNodes(group, "OperatorRef") {
				if operatorRef := nodeAttr(ref, "ref"); operatorRef != "" {
					operatorGroups[id] = append(operatorGroups[id], operatorRef)
				}
			}
		}
	}
	lineGroups := map[string][]string{}
	lineRouteRefs := map[string][]string{}
	for _, serviceFrame := range serviceFrames {
		for _, group := range findNodes(serviceFrame, "GroupOfLines") {
			id := nodeAttr(group, "id")
			if id == "" {
				continue
			}
			for _, ref := range findNodes(group, "LineRef") {
				if lineRef := nodeAttr(ref, "ref"); lineRef != "" {
					lineGroups[id] = append(lineGroups[id], lineRef)
				}
			}
		}
		for _, line := range findNodes(serviceFrame, "Line") {
			id := nodeAttr(line, "id")
			if id == "" {
				continue
			}
			refs := []string{id, netexReferenceLocalID(id)}
			if publicCode := nodeText(line, "PublicCode"); publicCode != "" {
				refs = append(refs, publicCode)
			}
			lineRouteRefs[id] = uniqueStrings(refs)
		}
	}

	locations := map[string]string{}
	for _, frame := range frames {
		for _, zone := range findNodes(frame, "FareZone") {
			sourceID := nodeAttr(zone, "id")
			if sourceID == "" {
				return report, fmt.Errorf("NeTEx FareZone has no id")
			}
			stopRefs := make([]string, 0)
			for _, stopRef := range findNodes(zone, "ScheduledStopPointRef") {
				atco := strings.TrimPrefix(nodeAttr(stopRef, "ref"), "atco:")
				if atco != "" {
					stopRefs = append(stopRefs, fmt.Sprintf(ctdf.GBStopIDFormat, atco))
				}
			}
			scopedSourceID := format.scopeSourceID(sourceID)
			locationRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeZone, scopedSourceID)
			location := &ctdf.FareLocation{
				FareRecord: baseRecord("location", sourceID),
				Name:       nodeText(zone, "Name"),
				Type:       ctdf.FareLocationTypeZone,
				StopRefs:   uniqueStrings(stopRefs),
			}
			location.PrimaryIdentifier = locationRef
			if err := upsertFareRecord("fare_locations", locationRef, location); err != nil {
				return report, err
			}
			locations[scopedSourceID] = locationRef
			report.ImportedFareLocations++
			stageNumber := nodeText(zone, "PublicCode")
			if stageNumber == "" {
				for _, code := range findNodes(zone, "PrivateCode") {
					if strings.Contains(strings.ToLower(nodeAttr(code, "type")), "farestagenumber") {
						stageNumber = strings.TrimSpace(code.Text)
						break
					}
				}
			}
			if stageNumber != "" {
				stageRef := ctdf.FareLocationIdentifier(dataset.Identifier, ctdf.FareLocationTypeStage, format.scopeSourceID(stageNumber))
				stage := &ctdf.FareLocation{
					FareRecord: ctdf.FareRecord{
						PrimaryIdentifier: stageRef,
						DatasetID:         dataset.Identifier,
						FareVersionID:     datasource.FareVersionID,
						SourceIdentifier:  stageNumber,
						DataSource:        datasource,
						CreationDateTime:  time.Now(),
					},
					Name:      nodeText(zone, "Name"),
					Type:      ctdf.FareLocationTypeStage,
					StopRefs:  uniqueStrings(stopRefs),
					ParentRef: locationRef,
				}
				if err := upsertFareRecord("fare_locations", stageRef, stage); err != nil {
					return report, err
				}
				report.ImportedFareLocations++
			}
		}
	}

	riderRefs := map[string]string{}
	for _, frame := range frames {
		for _, profile := range findNodes(frame, "UserProfile") {
			sourceID := nodeAttr(profile, "id")
			name := nodeText(profile, "Name")
			if sourceID == "" || name == "" {
				continue
			}
			scopedSourceID := format.scopeSourceID(sourceID)
			if riderRefs[scopedSourceID] != "" {
				continue
			}
			riderRef := netexID(dataset.Identifier, "rider", scopedSourceID)
			minimumAge, err := nodeOptionalInt(profile, "MinimumAge")
			if err != nil {
				return report, fmt.Errorf("NeTEx user profile %q minimum age: %w", sourceID, err)
			}
			maximumAge, err := nodeOptionalInt(profile, "MaximumAge")
			if err != nil {
				return report, fmt.Errorf("NeTEx user profile %q maximum age: %w", sourceID, err)
			}
			rider := &ctdf.FareRiderCategory{
				FareRecord:     baseRecord("rider", sourceID),
				Name:           name,
				UserType:       nodeText(profile, "UserType"),
				MinimumAge:     minimumAge,
				MaximumAge:     maximumAge,
				ProofRequired:  strings.Join(nodeTexts(profile, "ProofRequired"), ", "),
				EligibilityURL: nodeText(profile, "URL"),
				IsDefault:      strings.EqualFold(nodeText(profile, "UserType"), "adult"),
			}
			rider.PrimaryIdentifier = riderRef
			if err := upsertFareRecord("fare_rider_categories", riderRef, rider); err != nil {
				return report, err
			}
			riderRefs[scopedSourceID] = riderRef
			report.ImportedFareRiderCategories++
		}
	}

	mediaRefs := map[string][]string{}
	packageNames := map[string]string{}
	packageConditions := map[string][]string{}
	salesPackageProducts := map[string][]string{}
	for _, frame := range frames {
		for _, salesPackage := range findNodes(frame, "SalesOfferPackage") {
			packageID := nodeAttr(salesPackage, "id")
			if packageID == "" {
				continue
			}
			scopedPackageID := format.scopeSourceID(packageID)
			packageNames[scopedPackageID] = nodeText(salesPackage, "Name")
			salesPackageProducts[scopedPackageID] = salesOfferProductRefs(salesPackage)
			for _, assignment := range findNodes(salesPackage, "DistributionAssignment") {
				for _, payment := range nodeTexts(assignment, "PaymentMethods") {
					packageConditions[scopedPackageID] = append(packageConditions[scopedPackageID], "Payment method: "+payment)
				}
				if channel := nodeText(assignment, "DistributionChannelType"); channel != "" {
					packageConditions[scopedPackageID] = append(packageConditions[scopedPackageID], "Sales channel: "+channel)
				}
			}
			packageConditions[scopedPackageID] = uniqueStrings(packageConditions[scopedPackageID])
			for _, documentRef := range findNodes(salesPackage, "TypeOfTravelDocumentRef") {
				documentID := nodeAttr(documentRef, "ref")
				if documentID == "" {
					continue
				}
				scopedDocumentID := format.scopeSourceID(documentID)
				mediaSourceID := scopedPackageID + "|" + scopedDocumentID
				mediaRef := netexID(dataset.Identifier, "media", mediaSourceID)
				media := &ctdf.FareMedia{
					FareRecord: baseRecord("media", mediaSourceID),
					Name:       fareMediaName(documentID),
					Type:       fareMediaType(documentID),
				}
				media.PrimaryIdentifier = mediaRef
				if err := upsertFareRecord("fare_media", mediaRef, media); err != nil {
					return report, err
				}
				mediaRefs[scopedPackageID] = append(mediaRefs[scopedPackageID], mediaRef)
				report.ImportedFareMedia++
			}
		}
	}

	products := map[string]*ctdf.FareProduct{}
	for _, frame := range frames {
		for _, productNode := range append(findNodes(frame, "PreassignedFareProduct"), findNodes(frame, "AmountOfPriceUnitProduct")...) {
			sourceID := nodeAttr(productNode, "id")
			name := nodeText(productNode, "Name")
			if sourceID == "" || name == "" {
				return report, fmt.Errorf("NeTEx fare product must have an id and name")
			}
			productType := productTypeFromNode(productNode)
			productSourceID := format.scopeSourceID(sourceID)
			productRef := netexID(dataset.Identifier, "product", productSourceID)
			product := &ctdf.FareProduct{
				FareRecord: baseRecord("product", sourceID),
				Name:       name,
				Type:       productType,
			}
			product.PrimaryIdentifier = productRef
			products[productSourceID] = product
			if err := upsertFareRecord("fare_products", productRef, product); err != nil {
				return report, err
			}
			report.ImportedFareProducts++
		}
	}
	if len(products) == 0 {
		return report, fmt.Errorf("NeTEx FareFrame contains no supported fare products")
	}

	tariffs := map[string]*ctdf.FareTariff{}
	tariffNodes := map[string]*xmlNode{}
	for _, frame := range frames {
		for _, tariffNode := range findNodes(frame, "Tariff") {
			sourceID := nodeAttr(tariffNode, "id")
			if sourceID == "" {
				return report, fmt.Errorf("NeTEx Tariff has no id")
			}
			from, _, err := nodeDate(tariffNode, "FromDate")
			if err != nil {
				return report, fmt.Errorf("NeTEx Tariff %s FromDate: %w", sourceID, err)
			}
			to, toIsDate, err := nodeDate(tariffNode, "ToDate")
			if err != nil {
				return report, fmt.Errorf("NeTEx Tariff %s ToDate: %w", sourceID, err)
			}
			tariff := &ctdf.FareTariff{
				FareRecord:            baseRecord("tariff", sourceID),
				Name:                  nodeText(tariffNode, "Name"),
				SalesValidFrom:        from,
				SalesValidTo:          to,
				SalesValidToExclusive: exclusiveDateEnd(to, toIsDate),
				Currency:              frameCurrency(frame),
				Timezone:              "Europe/London",
			}
			scopedSourceID := format.scopeSourceID(sourceID)
			tariff.PrimaryIdentifier = netexID(dataset.Identifier, "tariff", scopedSourceID)
			tariffs[scopedSourceID] = tariff
			tariffNodes[scopedSourceID] = tariffNode
			if err := upsertFareRecord("fare_tariffs", tariff.PrimaryIdentifier, tariff); err != nil {
				return report, err
			}
			report.ImportedFareTariffs++
		}
	}

	amounts := map[string]ctdf.FareMoney{}
	priceGroups := map[string][]string{}
	matrix := map[string]netexDistanceMatrixElement{}
	for _, frame := range frames {
		frameAmounts, err := fareFrameAmounts(frame, format.scopeSourceID)
		if err != nil {
			return report, err
		}
		for id, amount := range frameAmounts {
			amounts[id] = amount
		}
		for _, group := range findNodes(frame, "PriceGroup") {
			groupID := nodeAttr(group, "id")
			if groupID == "" {
				continue
			}
			priceGroups[format.scopeSourceID(groupID)] = farePriceIDs(group)
		}
		for _, element := range findNodes(frame, "DistanceMatrixElement") {
			id := nodeAttr(element, "id")
			from := nodeAttr(firstNode(element, "StartTariffZoneRef"), "ref")
			to := nodeAttr(firstNode(element, "EndTariffZoneRef"), "ref")
			if id != "" && from != "" && to != "" {
				groupIDs := make([]string, 0)
				for _, groupRef := range findNodes(element, "PriceGroupRef") {
					if groupID := nodeAttr(groupRef, "ref"); groupID != "" {
						groupIDs = append(groupIDs, groupID)
					}
				}
				matrix[format.scopeSourceID(id)] = netexDistanceMatrixElement{from: from, to: to, priceGroupIDs: uniqueStrings(groupIDs)}
			}
		}
	}

	// Build a price for each tariff/product/OD matrix pair. Fare tables may
	// repeat the same price in multiple cells; the compound id makes reimports
	// and repeated modular frames idempotent.
	seenRules := map[string]bool{}
	tableCount := 0
	matchedProductTableCount := 0
	unpricedTableCount := 0
	unmatchedProductTables := make([]string, 0)
	for _, frame := range frames {
		for _, table := range findFareTables(frame) {
			tableCount++
			tableSourceID := format.scopeSourceID(nodeAttr(table, "id"))
			salesPackageID := nodeAttr(firstRef(table, "SalesOfferPackageRef"), "ref")
			productSourceID, err := fareTableProductSourceID(table, salesPackageProducts[format.scopeSourceID(salesPackageID)])
			if err != nil {
				return report, fmt.Errorf("NeTEx fare table %q: %w", nodeAttr(table, "id"), err)
			}
			product := products[format.scopeSourceID(productSourceID)]
			if product == nil {
				targets := fareTableTargetRefs(table)
				if len(targets) == 0 {
					targets = []string{"none"}
				}
				unmatchedProductTables = append(unmatchedProductTables, nodeAttr(table, "id")+"["+strings.Join(targets, ",")+"]")
				continue
			}
			matchedProductTableCount++
			tariffSourceID := nodeAttr(firstRef(table, "TariffRef"), "ref")
			var tariff *ctdf.FareTariff
			var tariffNode *xmlNode
			if tariffSourceID != "" {
				tariff = tariffs[format.scopeSourceID(tariffSourceID)]
				tariffNode = tariffNodes[format.scopeSourceID(tariffSourceID)]
			}
			if tariffSourceID != "" && (tariff == nil || tariffNode == nil) {
				return report, fmt.Errorf("NeTEx fare table references unknown tariff %q", tariffSourceID)
			}
			userProfileIDs := nodeRefValues(table, "UserProfileRef")
			tableRiderRefs := make([]string, 0, len(userProfileIDs))
			for _, userProfileID := range userProfileIDs {
				riderRef := riderRefs[format.scopeSourceID(userProfileID)]
				if riderRef == "" {
					return report, fmt.Errorf("NeTEx fare table references unknown user profile %q", userProfileID)
				}
				tableRiderRefs = append(tableRiderRefs, riderRef)
			}
			if len(tableRiderRefs) == 0 {
				tableRiderRefs = []string{""}
			}
			prices, err := fareTablePrices(table, matrix, priceGroups, locations, format.scopeSourceID)
			if err != nil {
				return report, err
			}
			if len(prices) == 0 {
				unpricedTableCount++
			}
			routeRefs := tariffRouteRefs(table, lineGroups, lineRouteRefs)
			if len(routeRefs) == 0 {
				routeRefs = tariffRouteRefs(tariffNode, lineGroups, lineRouteRefs)
			}
			if len(routeRefs) == 0 {
				routeRefs = []string{""}
			}
			operatorRefs := tariffOperatorRefs(tariffNode, operatorGroups)
			if len(operatorRefs) == 0 {
				operatorRefs = append(operatorRefs, format.SourceOperators...)
			}
			if len(operatorRefs) == 0 {
				return report, fmt.Errorf("NeTEx tariff %q has no resolvable operator", tariffSourceID)
			}
			tableMediaRefs := mediaRefs[format.scopeSourceID(salesPackageID)]
			if len(tableMediaRefs) == 0 {
				tableMediaRefs = []string{""}
			}
			for _, riderRef := range tableRiderRefs {
				for _, price := range prices {
					scopedPriceID := format.scopeSourceID(price.priceID)
					money, amountExists := amounts[scopedPriceID]
					if !amountExists {
						return report, fmt.Errorf("NeTEx fare table references unknown price %q", price.priceID)
					}
					tariffRef := ""
					if tariff != nil {
						tariffRef = tariff.PrimaryIdentifier
					}
					scopedPackageID := format.scopeSourceID(salesPackageID)
					sourceKey := strings.Join([]string{product.PrimaryIdentifier, tariffRef, tableSourceID, scopedPackageID, format.scopeSourceID(price.matrixID), scopedPriceID, riderRef}, "\x00")
					offerRef := netexID(dataset.Identifier, "offer", sourceKey)
					offer := &ctdf.FareOffer{
						FareRecord:            baseRecord("offer", sourceKey),
						TariffRef:             tariffRef,
						ProductRef:            product.PrimaryIdentifier,
						Price:                 money,
						Availability:          ctdf.FareAvailabilityUnknown,
						SalesOfferPackage:     packageNames[scopedPackageID],
						SalesConditions:       packageConditions[scopedPackageID],
						SalesValidFrom:        tariffDate(tariff, true),
						SalesValidTo:          tariffDate(tariff, false),
						SalesValidToExclusive: tariffDateExclusive(tariff),
					}
					offer.PrimaryIdentifier = offerRef
					if err := upsertFareRecord("fare_offers", offerRef, offer); err != nil {
						return report, err
					}
					report.ImportedFareOffers++
					for _, routeRef := range routeRefs {
						for _, operatorRef := range operatorRefs {
							for _, mediaRef := range tableMediaRefs {
								ruleKey := strings.Join([]string{product.PrimaryIdentifier, price.originRef, price.destinationRef, format.scopeSourceID(price.matrixID), routeRef, operatorRef, riderRef, mediaRef, offerRef}, "\x00")
								if seenRules[ruleKey] {
									continue
								}
								seenRules[ruleKey] = true
								ruleRef := netexID(dataset.Identifier, "rule", ruleKey)
								rule := &ctdf.FareLegRule{
									FareRecord:     baseRecord("rule", ruleKey),
									LegGroupRef:    netexID(dataset.Identifier, "leg-group", product.PrimaryIdentifier),
									ProductRef:     product.PrimaryIdentifier,
									OfferRef:       offerRef,
									OriginRef:      price.originRef,
									DestinationRef: price.destinationRef,
									RouteRef:       routeRef,
									OperatorRef:    netexOperatorRef(operatorRef),
									RiderRef:       riderRef,
									MediaRef:       mediaRef,
								}
								if err := upsertFareRecord("fare_leg_rules", ruleRef, rule); err != nil {
									return report, err
								}
								report.ImportedFareRules++
							}
						}
					}
				}
			}
		}
	}
	if report.ImportedFareRules == 0 {
		if len(unmatchedProductTables) > 5 {
			unmatchedProductTables = append(unmatchedProductTables[:5], "...")
		}
		return report, fmt.Errorf("NeTEx fare frames contain no supported priced fare rules (products=%d, tables=%d, tablesWithSupportedProducts=%d, tablesWithoutPrices=%d, unmatchedTableTargets=%v)", len(products), tableCount, matchedProductTableCount, unpricedTableCount, unmatchedProductTables)
	}
	return report, nil
}

func fareTableTargetRefs(table *xmlNode) []string {
	targets := make([]string, 0)
	for _, name := range []string{"PreassignedFareProductRef", "AmountOfPriceUnitProductRef", "CappedDiscountRightRef"} {
		for _, ref := range findNodes(table, name) {
			if id := nodeAttr(ref, "ref"); id != "" {
				targets = append(targets, name+"="+id)
			}
		}
	}
	return uniqueStrings(targets)
}

func fareTableProductSourceID(table *xmlNode, salesPackageProducts []string) (string, error) {
	productID := nodeAttr(firstRef(table, "PreassignedFareProductRef"), "ref")
	if productID == "" {
		productID = nodeAttr(firstRef(table, "AmountOfPriceUnitProductRef"), "ref")
	}
	if productID != "" {
		return productID, nil
	}
	salesPackageProducts = uniqueStrings(salesPackageProducts)
	switch len(salesPackageProducts) {
	case 0:
		return "", nil
	case 1:
		return salesPackageProducts[0], nil
	default:
		return "", fmt.Errorf("prices for a sales offer package containing multiple fare products are ambiguous")
	}
}

func salesOfferProductRefs(salesPackage *xmlNode) []string {
	productIDs := append(nodeRefValues(salesPackage, "PreassignedFareProductRef"), nodeRefValues(salesPackage, "AmountOfPriceUnitProductRef")...)
	return uniqueStrings(productIDs)
}

func fareFrameAmounts(frame *xmlNode, scopeSourceID func(string) string) (map[string]ctdf.FareMoney, error) {
	amounts := make(map[string]ctdf.FareMoney)
	currency := frameCurrency(frame)
	for _, priceType := range []string{"FarePrice", "GeographicalIntervalPrice", "DistanceMatrixElementPrice"} {
		for _, price := range findNodes(frame, priceType) {
			id, amount := nodeAttr(price, "id"), nodeText(price, "Amount")
			if id == "" || amount == "" {
				continue
			}
			money, err := moneyFromDecimal(amount, currency)
			if err != nil {
				return nil, fmt.Errorf("NeTEx price %s: %w", id, err)
			}
			amounts[scopeSourceID(id)] = money
		}
	}
	return amounts, nil
}

func findFareTables(root *xmlNode) []*xmlNode {
	tables := make([]*xmlNode, 0)
	var visit func(*xmlNode)
	visit = func(node *xmlNode) {
		if node.Name.Local == "FareTable" {
			tables = append(tables, node)
			return
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(root)
	return tables
}

func fareTablePrices(table *xmlNode, matrix map[string]netexDistanceMatrixElement, priceGroups map[string][]string, locations map[string]string, scopeSourceID func(string) string) ([]netexFareTablePrice, error) {
	cells := findNodes(table, "Cell")
	if len(cells) == 0 {
		cells = []*xmlNode{table}
	}
	prices := make([]netexFareTablePrice, 0)
	for _, cell := range cells {
		matrixID := nodeAttr(firstRef(cell, "DistanceMatrixElementRef"), "ref")
		priceIDs := make([]string, 0)
		groupIDs := make([]string, 0)
		priceIDs = append(priceIDs, farePriceIDs(cell)...)
		for _, groupRef := range findNodes(cell, "PriceGroupRef") {
			if id := nodeAttr(groupRef, "ref"); id != "" {
				groupIDs = append(groupIDs, id)
			}
		}
		var matrixElement netexDistanceMatrixElement
		if matrixID != "" {
			var exists bool
			matrixElement, exists = matrix[scopeSourceID(matrixID)]
			if !exists {
				return nil, fmt.Errorf("NeTEx fare table references unknown distance matrix element %q", matrixID)
			}
			if len(groupIDs) == 0 && len(priceIDs) == 0 {
				groupIDs = append(groupIDs, matrixElement.priceGroupIDs...)
			}
		}
		for _, groupID := range uniqueStrings(groupIDs) {
			members, exists := priceGroups[scopeSourceID(groupID)]
			if !exists {
				return nil, fmt.Errorf("NeTEx fare table references unknown price group %q", groupID)
			}
			priceIDs = append(priceIDs, members...)
		}
		priceIDs = uniqueStrings(priceIDs)
		for _, priceID := range priceIDs {
			entry := netexFareTablePrice{matrixID: matrixID, priceID: priceID}
			if matrixID != "" {
				entry.originRef = locations[scopeSourceID(matrixElement.from)]
				entry.destinationRef = locations[scopeSourceID(matrixElement.to)]
				if entry.originRef == "" || entry.destinationRef == "" {
					return nil, fmt.Errorf("NeTEx distance matrix %q references an unknown fare zone", matrixID)
				}
			}
			prices = append(prices, entry)
		}
	}
	return prices, nil
}

// farePriceIDs collects both NeTEx price reference forms and inline prices.
// Flat BODS fares use FarePrice while zone and point-to-point tables generally
// use GeographicalIntervalPrice.
func farePriceIDs(root *xmlNode) []string {
	ids := make([]string, 0)
	for _, name := range []string{"FarePriceRef", "GeographicalIntervalPriceRef"} {
		for _, ref := range findNodes(root, name) {
			if id := nodeAttr(ref, "ref"); id != "" {
				ids = append(ids, id)
			}
		}
	}
	for _, name := range []string{"FarePrice", "GeographicalIntervalPrice"} {
		for _, price := range findNodes(root, name) {
			if id := nodeAttr(price, "id"); id != "" {
				ids = append(ids, id)
			}
		}
	}
	for _, price := range findNodes(root, "DistanceMatrixElementPrice") {
		if nodeText(price, "Amount") == "" {
			continue
		}
		if id := nodeAttr(price, "id"); id != "" {
			ids = append(ids, id)
		}
	}
	return uniqueStrings(ids)
}

func findNodes(root *xmlNode, name string) []*xmlNode {
	if root == nil {
		return nil
	}
	result := make([]*xmlNode, 0)
	var visit func(*xmlNode)
	visit = func(node *xmlNode) {
		if node.Name.Local == name {
			result = append(result, node)
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(root)
	return result
}

func firstNode(root *xmlNode, name string) *xmlNode {
	for _, child := range root.Children {
		if child.Name.Local == name {
			return child
		}
	}
	return nil
}

func firstRef(root *xmlNode, name string) *xmlNode {
	for _, node := range findNodes(root, name) {
		if nodeAttr(node, "ref") != "" {
			return node
		}
	}
	return nil
}

func nodeRefValues(root *xmlNode, name string) []string {
	values := make([]string, 0)
	for _, node := range findNodes(root, name) {
		if ref := nodeAttr(node, "ref"); ref != "" {
			values = append(values, ref)
		}
	}
	return uniqueStrings(values)
}

func nodeAttr(node *xmlNode, name string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attrs {
		if attr.Name.Local == name {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func nodeText(root *xmlNode, name string) string {
	if root == nil {
		return ""
	}
	for _, child := range root.Children {
		if child.Name.Local == name {
			return strings.TrimSpace(child.Text)
		}
	}
	return ""
}

func nodeTexts(root *xmlNode, name string) []string {
	values := make([]string, 0)
	for _, node := range findNodes(root, name) {
		if value := strings.TrimSpace(node.Text); value != "" {
			values = append(values, value)
		}
	}
	return uniqueStrings(values)
}

func nodeOptionalInt(root *xmlNode, name string) (*int, error) {
	value := nodeText(root, name)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("invalid integer %q", value)
	}
	return &parsed, nil
}

func nodeDate(root *xmlNode, name string) (time.Time, bool, error) {
	value := nodeDescText(root, name)
	if value == "" {
		return time.Time{}, false, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, false, nil
		}
	}
	// NeTEx timezone-less dates and dateTimes in the UK fare profile use UK
	// local calendar time. Keep date-only precision so end dates remain
	// inclusive without treating every midnight instant as a whole day.
	location, err := time.LoadLocation("Europe/London")
	if err != nil {
		location = time.UTC
	}
	if parsed, parseErr := time.ParseInLocation("2006-01-02", value, location); parseErr == nil {
		return parsed, true, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if parsed, parseErr := time.ParseInLocation(layout, value, location); parseErr == nil {
			return parsed, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid date %q", value)
}

func exclusiveDateEnd(date time.Time, dateOnly bool) time.Time {
	if date.IsZero() || !dateOnly {
		return time.Time{}
	}
	return date.AddDate(0, 0, 1)
}

func nodeDescText(root *xmlNode, name string) string {
	for _, node := range findNodes(root, name) {
		return strings.TrimSpace(node.Text)
	}
	return ""
}

func frameCurrency(frame *xmlNode) string {
	currency := nodeDescText(frame, "DefaultCurrency")
	if currency == "" {
		currency = "GBP"
	}
	return currency
}

func moneyFromDecimal(value, currency string) (ctdf.FareMoney, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || parts[0] == "" {
		return ctdf.FareMoney{}, fmt.Errorf("invalid decimal amount %q", value)
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return ctdf.FareMoney{}, fmt.Errorf("invalid decimal amount %q", value)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return ctdf.FareMoney{}, fmt.Errorf("amount %q has more than two decimal places", value)
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	minor := int64(0)
	if fraction != "" {
		minor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return ctdf.FareMoney{}, fmt.Errorf("invalid decimal amount %q", value)
		}
	}
	if whole > (int64(^uint64(0)>>1)-minor)/100 {
		return ctdf.FareMoney{}, fmt.Errorf("amount %q overflows minor units", value)
	}
	money := ctdf.FareMoney{AmountMinor: whole*100 + minor, Currency: currency, Exponent: 2}
	if err := money.Validate(); err != nil {
		return ctdf.FareMoney{}, err
	}
	return money, nil
}

func productTypeFromNode(node *xmlNode) ctdf.FareProductType {
	switch strings.ToLower(nodeText(node, "ProductType")) {
	case "singletrip", "shorttrip", "timelimitedsingletrip", "multisteptrip":
		return ctdf.FareProductTypeSingleRide
	case "dayreturntrip", "periodreturntrip":
		return ctdf.FareProductTypeReturn
	case "daypass":
		return ctdf.FareProductTypePass
	case "periodpass":
		return ctdf.FareProductTypeSeason
	case "tripcarnet", "passcarnet":
		return ctdf.FareProductTypeMultiTrip
	default:
		return ctdf.FareProductTypeOther
	}
}

func fareMediaType(documentID string) ctdf.FareMediaType {
	value := strings.ToLower(netexReferenceLocalID(documentID))
	switch {
	case strings.Contains(value, "paper"):
		return ctdf.FareMediaTypePaper
	case strings.Contains(value, "mobile"), strings.Contains(value, "app"), strings.Contains(value, "m_ticket"), strings.Contains(value, "mticket"):
		return ctdf.FareMediaTypeApp
	case strings.Contains(value, "contactless"):
		return ctdf.FareMediaTypeContactless
	case strings.Contains(value, "card"), strings.Contains(value, "smart"):
		return ctdf.FareMediaTypeTransitCard
	default:
		return ctdf.FareMediaTypeOther
	}
}

func fareMediaName(documentID string) string {
	switch fareMediaType(documentID) {
	case ctdf.FareMediaTypePaper:
		return "Paper ticket"
	case ctdf.FareMediaTypeApp:
		return "App ticket"
	case ctdf.FareMediaTypeContactless:
		return "Contactless"
	case ctdf.FareMediaTypeTransitCard:
		return "Transit card"
	default:
		return netexReferenceLocalID(documentID)
	}
}

func netexID(datasetID, kind, sourceID string) string {
	hash := sha256.Sum256([]byte(sourceID))
	return fmt.Sprintf("%s-netex-%s-%s", datasetID, kind, hex.EncodeToString(hash[:]))
}

func (format *Fares) scopeSourceID(sourceID string) string {
	if format.SourceNamespace == "" {
		return sourceID
	}
	return format.SourceNamespace + ":" + sourceID
}

func netexOperatorRef(sourceID string) string {
	if strings.HasPrefix(sourceID, "gb-noc-") {
		return sourceID
	}
	code := strings.TrimPrefix(sourceID, "noc:")
	if separator := strings.Index(code, ":noc:"); separator >= 0 {
		code = code[:separator]
	}
	if code == "" {
		return ""
	}
	return "gb-noc-" + code
}

func uniqueStrings(values []string) []string {
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

func tariffDate(tariff *ctdf.FareTariff, start bool) time.Time {
	if tariff == nil {
		return time.Time{}
	}
	if start {
		return tariff.SalesValidFrom
	}
	return tariff.SalesValidTo
}

func tariffDateExclusive(tariff *ctdf.FareTariff) time.Time {
	if tariff == nil {
		return time.Time{}
	}
	return tariff.SalesValidToExclusive
}

func tariffRouteRefs(tariff *xmlNode, lineGroups, lineRouteRefs map[string][]string) []string {
	if tariff == nil {
		return nil
	}
	refs := make([]string, 0)
	for _, node := range findNodes(tariff, "LineRef") {
		if ref := nodeAttr(node, "ref"); ref != "" {
			refs = append(refs, lineIdentifiers(ref, lineRouteRefs)...)
		}
	}
	for _, node := range findNodes(tariff, "GroupOfLinesRef") {
		if ref := nodeAttr(node, "ref"); ref != "" {
			members := lineGroups[ref]
			if len(members) == 0 {
				// Retaining an unresolved group as a route restriction keeps it
				// from accidentally broadening into an operator-wide fare.
				refs = append(refs, lineIdentifiers(ref, lineRouteRefs)...)
			} else {
				for _, member := range members {
					refs = append(refs, lineIdentifiers(member, lineRouteRefs)...)
				}
			}
		}
	}
	return uniqueStrings(refs)
}

func lineIdentifiers(ref string, lineRouteRefs map[string][]string) []string {
	if aliases := lineRouteRefs[ref]; len(aliases) > 0 {
		return aliases
	}
	return uniqueStrings([]string{ref, netexReferenceLocalID(ref)})
}

func netexReferenceLocalID(ref string) string {
	if index := strings.LastIndex(ref, ":"); index >= 0 && index+1 < len(ref) {
		return ref[index+1:]
	}
	return ref
}

func tariffOperatorRefs(tariff *xmlNode, operatorGroups map[string][]string) []string {
	if tariff == nil {
		return nil
	}
	refs := make([]string, 0)
	for _, node := range findNodes(tariff, "OperatorRef") {
		if ref := nodeAttr(node, "ref"); ref != "" {
			refs = append(refs, ref)
		}
	}
	for _, node := range findNodes(tariff, "GroupOfOperatorsRef") {
		if ref := nodeAttr(node, "ref"); ref != "" {
			members := operatorGroups[ref]
			if len(members) == 0 {
				refs = append(refs, ref)
			} else {
				refs = append(refs, members...)
			}
		}
	}
	return uniqueStrings(refs)
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
	_, err := database.GetCollection(collectionName).ReplaceOne(ctx, bson.M{
		"primaryidentifier": identifier,
		"datasetid":         versioned.GetFareDatasetID(),
		"fareversionid":     versioned.GetFareVersionID(),
	}, value, mongooptions.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("store fare record %s: %w", identifier, err)
	}
	return nil
}
