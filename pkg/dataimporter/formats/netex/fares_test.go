package netex

import (
	"strings"
	"testing"
)

func TestFareTablePricesBODSPointToPointTable(t *testing.T) {
	format := &Fares{}
	input := `<PublicationDelivery><CompositeFrame><frames><FareFrame>
		<priceGroups><PriceGroup id="adult-prices"><members>
			<GeographicalIntervalPrice id="adult-120"><Amount>1.20</Amount></GeographicalIntervalPrice>
		</members></PriceGroup></priceGroups>
		<fareTables><FareTable id="main-table"><pricesFor>
			<PreassignedFareProductRef ref="adult-single"/>
		</pricesFor><includes><FareTable id="column-table"><cells><Cell>
			<DistanceMatrixElementPrice>
				<GeographicalIntervalPriceRef ref="adult-120"/>
				<DistanceMatrixElementRef ref="from-to"/>
			</DistanceMatrixElementPrice>
		</Cell></cells></FareTable></includes></FareTable></fareTables>
	</FareFrame></frames></CompositeFrame></PublicationDelivery>`
	if err := format.ParseFile(strings.NewReader(input)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	frame := findNodes(format.root, "FareFrame")[0]
	table := findNodes(frame, "FareTable")[0]
	productRef := nodeAttr(firstRef(table, "PreassignedFareProductRef"), "ref")
	if productRef != "adult-single" {
		t.Fatalf("PricesFor product ref = %q, want adult-single", productRef)
	}

	scope := func(id string) string { return "feed:" + id }
	prices, err := fareTablePrices(
		table,
		map[string]netexDistanceMatrixElement{"feed:from-to": {from: "from", to: "to"}},
		map[string][]string{},
		map[string]string{"feed:from": "origin", "feed:to": "destination"},
		scope,
	)
	if err != nil {
		t.Fatalf("fareTablePrices() error = %v", err)
	}
	if len(prices) != 1 {
		t.Fatalf("fareTablePrices() returned %d prices, want 1", len(prices))
	}
	want := netexFareTablePrice{
		matrixID:       "from-to",
		priceID:        "adult-120",
		originRef:      "origin",
		destinationRef: "destination",
	}
	if prices[0] != want {
		t.Errorf("fareTablePrices()[0] = %#v, want %#v", prices[0], want)
	}

	group := findNodes(frame, "PriceGroup")[0]
	if got := farePriceIDs(group); len(got) != 1 || got[0] != "adult-120" {
		t.Errorf("farePriceIDs(PriceGroup) = %v, want [adult-120]", got)
	}
}

func TestDiscountRightDetailsPreserveNeTExTypes(t *testing.T) {
	format := &Fares{}
	input := `<FareFrame>
		<SaleDiscountRight id="railcard-1" productType="travelCard"><Name>Network Railcard</Name><ProofRequired>Valid railcard</ProofRequired><URL>https://example.test/card</URL></SaleDiscountRight>
		<CappedDiscountRight id="oyster"><Name>Oyster</Name></CappedDiscountRight>
		<UsageDiscountRight id="mileage" productType="usageRebate"><Name>Mileage rebate</Name></UsageDiscountRight>
	</FareFrame>`
	if err := format.ParseFile(strings.NewReader(input)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	details := fareDiscountRightDetails(findNodes(format.root, "SaleDiscountRight")[0])
	if details.SourceID != "railcard-1" || details.Name != "Network Railcard" || details.ProductType != "SaleDiscountRight: travelCard" {
		t.Fatalf("sale discount right details = %#v", details)
	}
	if details.ProofRequired != "Valid railcard" || details.EligibilityURL != "https://example.test/card" {
		t.Fatalf("eligibility details = %#v", details)
	}
	capped := fareDiscountRightDetails(findNodes(format.root, "CappedDiscountRight")[0])
	usage := fareDiscountRightDetails(findNodes(format.root, "UsageDiscountRight")[0])
	if capped.ProductType != "CappedDiscountRight" || usage.ProductType != "UsageDiscountRight: usageRebate" {
		t.Fatalf("other discount right types = %q, %q", capped.ProductType, usage.ProductType)
	}
	if netexDiscountRightSourcePrefix("SaleDiscountRight") != "sale-discount-right" ||
		netexDiscountRightSourcePrefix("CappedDiscountRight") != "capped-discount-right" ||
		netexDiscountRightSourcePrefix("UsageDiscountRight") != "usage-discount-right" {
		t.Fatal("discount right source prefixes are not stable")
	}
}

func TestFareTablePricesFlatPrices(t *testing.T) {
	format := &Fares{}
	input := `<FareFrame><FareTable id="flat-table"><pricesFor>
		<PreassignedFareProductRef ref="day-ticket"/>
	</pricesFor><cells>
		<Cell><FarePriceRef ref="cash-price"/></Cell>
		<Cell><FarePrice id="app-price"><Amount>2.50</Amount></FarePrice></Cell>
	</cells></FareTable></FareFrame>`
	if err := format.ParseFile(strings.NewReader(input)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	frame := findNodes(format.root, "FareFrame")[0]
	table := findNodes(frame, "FareTable")[0]
	prices, err := fareTablePrices(table, nil, nil, nil, func(id string) string { return id })
	if err != nil {
		t.Fatalf("fareTablePrices() error = %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("fareTablePrices() returned %d prices, want 2", len(prices))
	}
	if prices[0].priceID != "cash-price" || prices[1].priceID != "app-price" {
		t.Errorf("flat price IDs = [%q %q], want [cash-price app-price]", prices[0].priceID, prices[1].priceID)
	}
	for _, price := range prices {
		if price.originRef != "" || price.destinationRef != "" {
			t.Errorf("flat price %q unexpectedly has origin/destination refs: %#v", price.priceID, price)
		}
	}

	productRef := nodeAttr(firstRef(table, "PreassignedFareProductRef"), "ref")
	if productRef != "day-ticket" {
		t.Errorf("PricesFor product ref = %q, want day-ticket", productRef)
	}
	amount, err := moneyFromDecimal("2.50", "GBP")
	if err != nil {
		t.Fatalf("moneyFromDecimal() error = %v", err)
	}
	if amount.AmountMinor != 250 || amount.Currency != "GBP" || amount.Exponent != 2 {
		t.Errorf("flat fare money = %#v, want 250 GBP minor units", amount)
	}
}

func TestFareTablePricesFromSalesPackageAndInlineMatrixPrice(t *testing.T) {
	format := &Fares{}
	input := `<PublicationDelivery><CompositeFrame><frames>
		<FareFrame id="product-frame"><fareProducts>
			<PreassignedFareProduct id="adult-single"><Name>Adult Single</Name></PreassignedFareProduct>
		</fareProducts><salesOfferPackages><SalesOfferPackage id="cash-package">
			<salesOfferPackageElements><SalesOfferPackageElement>
				<PreassignedFareProductRef ref="adult-single"/>
			</SalesOfferPackageElement></salesOfferPackageElements>
		</SalesOfferPackage></salesOfferPackages></FareFrame>
		<FareFrame id="price-frame"><FrameDefaults><DefaultCurrency>GBP</DefaultCurrency></FrameDefaults>
			<fareTables><FareTable id="fare-table"><includes><FareTable id="fare-table@adult">
				<pricesFor><SalesOfferPackageRef ref="cash-package"/></pricesFor>
				<limitations><UserProfileRef ref="adult"/></limitations>
				<prices><DistanceMatrixElementPrice id="adult-single-price"><Amount>3.00</Amount></DistanceMatrixElementPrice></prices>
			</FareTable></includes></FareTable></fareTables>
		</FareFrame>
	</frames></CompositeFrame></PublicationDelivery>`
	if err := format.ParseFile(strings.NewReader(input)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	frames := findNodes(format.root, "FareFrame")
	if len(frames) != 2 {
		t.Fatalf("found %d FareFrame nodes, want 2", len(frames))
	}
	salesPackage := findNodes(frames[0], "SalesOfferPackage")[0]
	productRefs := salesOfferProductRefs(salesPackage)
	if len(productRefs) != 1 || productRefs[0] != "adult-single" {
		t.Fatalf("salesOfferProductRefs() = %v, want [adult-single]", productRefs)
	}
	tables := findFareTables(frames[1])
	if len(tables) != 1 || nodeAttr(tables[0], "id") != "fare-table" {
		t.Fatalf("findFareTables() = %v, want only the including fare-table", tableIDs(tables))
	}
	table := tables[0]
	productID, err := fareTableProductSourceID(table, productRefs)
	if err != nil {
		t.Fatalf("fareTableProductSourceID() error = %v", err)
	}
	if productID != "adult-single" {
		t.Fatalf("fareTableProductSourceID() = %q, want adult-single", productID)
	}

	scope := func(id string) string { return "feed:" + id }
	prices, err := fareTablePrices(table, nil, nil, nil, scope)
	if err != nil {
		t.Fatalf("fareTablePrices() error = %v", err)
	}
	if len(prices) != 1 || prices[0].priceID != "adult-single-price" || prices[0].originRef != "" || prices[0].destinationRef != "" {
		t.Fatalf("fareTablePrices() = %#v, want one unscoped flat price", prices)
	}
	amounts, err := fareFrameAmounts(frames[1], scope)
	if err != nil {
		t.Fatalf("fareFrameAmounts() error = %v", err)
	}
	amount := amounts[scope("adult-single-price")]
	if amount.AmountMinor != 300 || amount.Currency != "GBP" || amount.Exponent != 2 {
		t.Errorf("fareFrameAmounts()[adult-single-price] = %#v, want 300 GBP minor units", amount)
	}
}

func tableIDs(tables []*xmlNode) []string {
	ids := make([]string, len(tables))
	for index, table := range tables {
		ids[index] = nodeAttr(table, "id")
	}
	return ids
}

func TestFareTableTargetRefsReportsCappedDiscountRight(t *testing.T) {
	format := &Fares{}
	input := `<FareFrame><FareTable id="capped-table"><pricesFor>
		<CappedDiscountRightRef ref="daily-cap"/>
		<SalesOfferPackageRef ref="mobile-package"/>
	</pricesFor></FareTable></FareFrame>`
	if err := format.ParseFile(strings.NewReader(input)); err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	table := findNodes(format.root, "FareTable")[0]
	want := []string{"CappedDiscountRightRef=daily-cap"}
	got := fareTableTargetRefs(table)
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("fareTableTargetRefs() = %v, want %v", got, want)
	}
}
