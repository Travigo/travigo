package ctdf

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FareRecord fields are repeated on imported fare records so a candidate
// catalogue can be validated and activated as one immutable version.
type FareRecord struct {
	PrimaryIdentifier string               `bson:"primaryidentifier"`
	DatasetID         string               `bson:"datasetid"`
	FareVersionID     string               `bson:"fareversionid"`
	SourceIdentifier  string               `bson:"sourceidentifier,omitempty"`
	DataSource        *DataSourceReference `bson:"datasource,omitempty"`
	EffectiveFrom     time.Time            `bson:"effectivefrom,omitempty"`
	EffectiveTo       time.Time            `bson:"effectiveto,omitempty"`
	CreationDateTime  time.Time            `bson:"creationdatetime"`
}

func (record FareRecord) GetFareDatasetID() string  { return record.DatasetID }
func (record FareRecord) GetFareVersionID() string  { return record.FareVersionID }
func (record FareRecord) GetFareRecord() FareRecord { return record }

type FareLocationType string

const (
	FareLocationTypeStop  FareLocationType = "stop"
	FareLocationTypeZone  FareLocationType = "zone"
	FareLocationTypeArea  FareLocationType = "area"
	FareLocationTypeGroup FareLocationType = "group"
	FareLocationTypeStage FareLocationType = "stage"
)

type FareLocation struct {
	FareRecord `bson:",inline"`
	Name       string           `bson:"name,omitempty"`
	Type       FareLocationType `bson:"type"`
	ParentRef  string           `bson:"parentref,omitempty"`
	StopRefs   []string         `bson:"stoprefs,omitempty"`
}

// FareLocationIdentifier returns a stable location ID shared by a fare source
// and the schedule records that refer to the same source location.
func FareLocationIdentifier(datasetID string, locationType FareLocationType, sourceID string) string {
	if datasetID == "" || sourceID == "" {
		return ""
	}
	return fmt.Sprintf("%s-fare-location-%s-%s", datasetID, locationType, sourceID)
}

type FareTariff struct {
	FareRecord            `bson:",inline"`
	Name                  string    `bson:"name"`
	Currency              string    `bson:"currency,omitempty"`
	SalesValidFrom        time.Time `bson:"salesvalidfrom,omitempty"`
	SalesValidTo          time.Time `bson:"salesvalidto,omitempty"`
	SalesValidToExclusive time.Time `bson:"salesvalidtoexclusive,omitempty"`
	UsageValidFrom        time.Time `bson:"usagevalidfrom,omitempty"`
	UsageValidTo          time.Time `bson:"usagevalidto,omitempty"`
	UsageValiditySec      int64     `bson:"usagevaliditysec,omitempty"`
	Timezone              string    `bson:"timezone,omitempty"`
}

type FareRiderCategory struct {
	FareRecord      `bson:",inline"`
	Name            string `bson:"name"`
	UserType        string `bson:"usertype,omitempty"`
	RailcardCode    string `bson:"railcardcode,omitempty"`
	AdultStatusCode string `bson:"adultstatuscode,omitempty"`
	ChildStatusCode string `bson:"childstatuscode,omitempty"`
	MinimumAge      *int   `bson:"minimumage,omitempty"`
	MaximumAge      *int   `bson:"maximumage,omitempty"`
	ProofRequired   string `bson:"proofrequired,omitempty"`
	IsDefault       bool   `bson:"isdefault,omitempty"`
	EligibilityURL  string `bson:"eligibilityurl,omitempty"`
}

func (rider FareRiderCategory) Validate() error {
	if err := rider.FareRecord.Validate(); err != nil {
		return err
	}
	if rider.Name == "" {
		return fmt.Errorf("fare rider category name is required")
	}
	if rider.MinimumAge != nil && *rider.MinimumAge < 0 {
		return fmt.Errorf("fare rider minimum age cannot be negative")
	}
	if rider.MaximumAge != nil && *rider.MaximumAge < 0 {
		return fmt.Errorf("fare rider maximum age cannot be negative")
	}
	if rider.MinimumAge != nil && rider.MaximumAge != nil && *rider.MaximumAge < *rider.MinimumAge {
		return fmt.Errorf("fare rider maximum age is less than its minimum age")
	}
	return nil
}

type FareMediaType string

const (
	FareMediaTypeNone        FareMediaType = "none"
	FareMediaTypePaper       FareMediaType = "paper"
	FareMediaTypeTransitCard FareMediaType = "transit_card"
	FareMediaTypeApp         FareMediaType = "app"
	FareMediaTypeContactless FareMediaType = "contactless"
	FareMediaTypeOther       FareMediaType = "other"
)

type FareMedia struct {
	FareRecord `bson:",inline"`
	Name       string        `bson:"name"`
	Type       FareMediaType `bson:"type"`
}

type FareNetwork struct {
	FareRecord `bson:",inline"`
	Name       string `bson:"name,omitempty"`
}

type FareRouteNetwork struct {
	FareRecord `bson:",inline"`
	RouteRef   string `bson:"routeref"`
	NetworkRef string `bson:"networkref"`
}

type FareTimeframe struct {
	FareRecord      `bson:",inline"`
	GroupRef        string        `bson:"groupref"`
	StartTimeSecond int64         `bson:"starttimesecond"`
	EndTimeSecond   int64         `bson:"endtimesecond"`
	ServiceRef      string        `bson:"serviceref,omitempty"`
	Availability    *Availability `bson:"availability,omitempty"`
	AddedDates      []time.Time   `bson:"addeddates,omitempty"`
	RemovedDates    []time.Time   `bson:"removeddates,omitempty"`
}

type FareProductType string

const (
	FareProductTypeSingleRide FareProductType = "single_ride"
	FareProductTypeReturn     FareProductType = "return"
	FareProductTypePass       FareProductType = "pass"
	FareProductTypeSeason     FareProductType = "season"
	FareProductTypeMultiTrip  FareProductType = "multi_trip"
	FareProductTypeOther      FareProductType = "other"
)

type FareClass string

const (
	FareClassStandard FareClass = "standard"
	FareClassFirst    FareClass = "first"
	FareClassBusiness FareClass = "business"
	FareClassOther    FareClass = "other"
)

type FareProduct struct {
	FareRecord              `bson:",inline"`
	ProductGroupRef         string          `bson:"productgroupref,omitempty"`
	Name                    string          `bson:"name"`
	Type                    FareProductType `bson:"type"`
	Class                   FareClass       `bson:"class,omitempty"`
	PaymentMethod           string          `bson:"paymentmethod,omitempty"`
	RiderRef                string          `bson:"riderref,omitempty"`
	MediaRef                string          `bson:"mediaref,omitempty"`
	MaxTransfers            *int            `bson:"maxtransfers,omitempty"`
	TransfersUnlimited      bool            `bson:"transfersunlimited,omitempty"`
	TransferDurationSeconds *int64          `bson:"transferdurationseconds,omitempty"`
	RailTicket              *FareRailTicket `bson:"railticket,omitempty"`
}

// FareRailTicket retains RSPS5045 ticket-type and validity attributes that
// affect how a published fare may be used. It is descriptive unless the
// planner has enough journey and passenger context to evaluate a condition.
type FareRailTicket struct {
	TicketCode        string              `bson:"ticketcode,omitempty"`
	ValidityCode      string              `bson:"validitycode,omitempty"`
	DiscountCategory  string              `bson:"discountcategory,omitempty"`
	LastValidDay      time.Time           `bson:"lastvalidday,omitempty"`
	Reservation       string              `bson:"reservation,omitempty"`
	MinimumPassengers int                 `bson:"minimumpassengers,omitempty"`
	MaximumPassengers int                 `bson:"maximumpassengers,omitempty"`
	Validity          *FareTicketValidity `bson:"validity,omitempty"`
	Calendars         []FareRailCalendar  `bson:"calendars,omitempty"`
}

type FareTicketValidity struct {
	Description         string `bson:"description,omitempty"`
	OutwardDays         int    `bson:"outwarddays,omitempty"`
	OutwardMonths       int    `bson:"outwardmonths,omitempty"`
	ReturnDays          int    `bson:"returndays,omitempty"`
	ReturnMonths        int    `bson:"returnmonths,omitempty"`
	ReturnAfterDays     int    `bson:"returnafterdays,omitempty"`
	ReturnAfterMonths   int    `bson:"returnaftermonths,omitempty"`
	ReturnAfterWeekday  string `bson:"returnafterweekday,omitempty"`
	BreakOutwardAllowed bool   `bson:"breakoutwardallowed,omitempty"`
	BreakReturnAllowed  bool   `bson:"breakreturnallowed,omitempty"`
	OutwardDescription  string `bson:"outwarddescription,omitempty"`
	ReturnDescription   string `bson:"returndescription,omitempty"`
}

type FareAvailability string

const (
	FareAvailabilityUnknown     FareAvailability = "unknown"
	FareAvailabilityAvailable   FareAvailability = "available"
	FareAvailabilityUnavailable FareAvailability = "unavailable"
)

// FareMoney stores signed values in integer minor units so fare products can
// represent GTFS Fares v2 transfer discounts. Final journey quotes and caps
// must still be non-negative. Exponent is retained because currencies differ.
type FareMoney struct {
	AmountMinor int64  `bson:"amountminor" json:"amount_minor" groups:"basic,web-planner"`
	Currency    string `bson:"currency" json:"currency" groups:"basic,web-planner"`
	Exponent    uint8  `bson:"exponent" json:"exponent" groups:"basic,web-planner"`
}

var currencyCodePattern = regexp.MustCompile(`^[A-Z]{3}$`)
var iso4217CurrencyCodes = func() map[string]struct{} {
	codes := strings.Fields("AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL BSD BTN BWP BYN BYR BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HRK HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SLL SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED VES VND VUV WST XAF XAG XAU XBA XBB XBC XBD XCD XCG XDR XOF XPD XPF XPT XSU XUA YER ZAR ZMW ZWG ZWL")
	result := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		result[code] = struct{}{}
	}
	return result
}()

func (m FareMoney) Validate() error {
	if !currencyCodePattern.MatchString(m.Currency) {
		return fmt.Errorf("currency must be an uppercase ISO 4217 code")
	}
	if _, exists := iso4217CurrencyCodes[m.Currency]; !exists {
		return fmt.Errorf("currency %q is not a recognized ISO 4217 code", m.Currency)
	}
	if m.Exponent > 4 {
		return fmt.Errorf("currency exponent must be between 0 and 4")
	}
	return nil
}

func (m FareMoney) ValidateNonNegative() error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.AmountMinor < 0 {
		return fmt.Errorf("fare amount cannot be negative")
	}
	return nil
}

type FareOffer struct {
	FareRecord            `bson:",inline"`
	TariffRef             string           `bson:"tariffref,omitempty"`
	ProductRef            string           `bson:"productref"`
	Price                 FareMoney        `bson:"price"`
	Availability          FareAvailability `bson:"availability"`
	SalesURL              string           `bson:"salesurl,omitempty"`
	SalesOfferPackage     string           `bson:"salesofferpackage,omitempty"`
	SalesConditions       []string         `bson:"salesconditions,omitempty"`
	SalesValidFrom        time.Time        `bson:"salesvalidfrom,omitempty"`
	SalesValidTo          time.Time        `bson:"salesvalidto,omitempty"`
	SalesValidToExclusive time.Time        `bson:"salesvalidtoexclusive,omitempty"`
}

type FareLegRule struct {
	FareRecord             `bson:",inline"`
	LegGroupRef            string              `bson:"leggroupref,omitempty"`
	ProductRef             string              `bson:"productref"`
	OfferRef               string              `bson:"offerref"`
	ProductRefs            []string            `bson:"productrefs,omitempty"`
	OfferRefs              []string            `bson:"offerrefs,omitempty"`
	OriginRef              string              `bson:"originref,omitempty"`
	DestinationRef         string              `bson:"destinationref,omitempty"`
	ViaRefs                []string            `bson:"viarefs,omitempty"`
	RouteRef               string              `bson:"routeref,omitempty"`
	NetworkRef             string              `bson:"networkref,omitempty"`
	OperatorRef            string              `bson:"operatorref,omitempty"`
	RulePriority           int                 `bson:"rulepriority,omitempty"`
	JourneyScope           bool                `bson:"journeyscope,omitempty"`
	EmptyFieldsAreFallback bool                `bson:"emptyfieldsarefallback,omitempty"`
	RiderRef               string              `bson:"riderref,omitempty"`
	MediaRef               string              `bson:"mediaref,omitempty"`
	FromTimeframeRef       string              `bson:"fromtimeframeref,omitempty"`
	ToTimeframeRef         string              `bson:"totimeframeref,omitempty"`
	UnresolvedConstraints  []string            `bson:"unresolvedconstraints,omitempty"`
	RailConditions         *FareRailConditions `bson:"railconditions,omitempty"`
}

type FareRailConditions struct {
	TicketCode           string                    `bson:"ticketcode,omitempty"`
	RouteCode            string                    `bson:"routecode,omitempty"`
	Route                *FareRailRoute            `bson:"route,omitempty"`
	RestrictionCode      string                    `bson:"restrictioncode,omitempty"`
	FareSettingTOC       string                    `bson:"faresettingtoc,omitempty"`
	CrossLondonIndicator string                    `bson:"crosslondonindicator,omitempty"`
	UsageCode            string                    `bson:"usagecode,omitempty"`
	NonStandardDiscount  bool                      `bson:"nonstandarddiscount,omitempty"`
	Restrictions         []FareRailRestriction     `bson:"restrictions,omitempty"`
	TicketCalendars      []FareRailCalendar        `bson:"ticketcalendars,omitempty"`
	AdvancePurchase      []FareRailAdvancePurchase `bson:"advancepurchase,omitempty"`
	TOCSpecificTickets   []FareRailTOCTicket       `bson:"tocspecifictickets,omitempty"`
}

type FareRailAdvancePurchase struct {
	RestrictionCode string    `bson:"restrictioncode,omitempty"`
	RestrictionFlag string    `bson:"restrictionflag"`
	TOCCode         string    `bson:"toccode,omitempty"`
	ValidFrom       time.Time `bson:"validfrom,omitempty"`
	ValidTo         time.Time `bson:"validto,omitempty"`
	CheckType       string    `bson:"checktype"`
	BookingDate     time.Time `bson:"bookingdate,omitempty"`
	AdvanceHours    int       `bson:"advancehours,omitempty"`
	AdvanceDays     int       `bson:"advancedays,omitempty"`
	BookingTime     int       `bson:"bookingtime,omitempty"`
	HasBookingTime  bool      `bson:"hasbookingtime,omitempty"`
}

type FareRailTOCTicket struct {
	RestrictionCode string    `bson:"restrictioncode,omitempty"`
	RestrictionFlag string    `bson:"restrictionflag"`
	Direction       string    `bson:"direction"`
	TOCCode         string    `bson:"toccode,omitempty"`
	TOCType         string    `bson:"toctype"`
	ValidFrom       time.Time `bson:"validfrom,omitempty"`
	ValidTo         time.Time `bson:"validto,omitempty"`
	SleeperRequired bool      `bson:"sleeperrequired,omitempty"`
	StockIndicator  string    `bson:"stockindicator,omitempty"`
	StockList       []string  `bson:"stocklist,omitempty"`
}

type FareRailRoute struct {
	Description string                  `bson:"description,omitempty"`
	Locations   []FareRailRouteLocation `bson:"locations,omitempty"`
}

type FareRailRouteLocation struct {
	NLC            string `bson:"nlc,omitempty"`
	CRS            string `bson:"crs,omitempty"`
	IncludeExclude string `bson:"includeexclude,omitempty"`
}

type FareRailRestriction struct {
	Code                   string                     `bson:"code"`
	Description            string                     `bson:"description,omitempty"`
	OutwardDescription     string                     `bson:"outwarddescription,omitempty"`
	ReturnDescription      string                     `bson:"returndescription,omitempty"`
	ValidFrom              time.Time                  `bson:"validfrom,omitempty"`
	ValidTo                time.Time                  `bson:"validto,omitempty"`
	OutwardMode            string                     `bson:"outwardmode,omitempty"`
	ReturnMode             string                     `bson:"returnmode,omitempty"`
	ChangeAllowed          bool                       `bson:"changeallowed,omitempty"`
	DateBands              []FareRailDateBand         `bson:"datebands,omitempty"`
	TimeRestrictions       []FareRailTimeRestriction  `bson:"timerestrictions,omitempty"`
	TrainRestrictions      []FareRailTrainRestriction `bson:"trainrestrictions,omitempty"`
	RouteLocations         []string                   `bson:"routelocations,omitempty"`
	AllowedChangeLocations []string                   `bson:"allowedchangelocations,omitempty"`
	Unsupported            []string                   `bson:"unsupported,omitempty"`
}

type FareRailDateBand struct {
	FromMonthDay string `bson:"frommonthday"`
	ToMonthDay   string `bson:"tomonthday"`
	Days         string `bson:"days,omitempty"`
}

type FareRailTimeRestriction struct {
	Sequence      string             `bson:"sequence"`
	OutRet        string             `bson:"outret,omitempty"`
	TimeFrom      int                `bson:"timefrom"`
	TimeTo        int                `bson:"timeto"`
	Event         string             `bson:"event,omitempty"`
	LocationCRS   string             `bson:"locationcrs,omitempty"`
	TimetableType string             `bson:"timetabletype,omitempty"`
	TrainType     string             `bson:"traintype,omitempty"`
	MinimumFare   bool               `bson:"minimumfare,omitempty"`
	TOCCodes      []string           `bson:"toccodes,omitempty"`
	DateBands     []FareRailDateBand `bson:"datebands,omitempty"`
}

type FareRailTrainRestriction struct {
	TrainUID  string             `bson:"trainuid"`
	OutRet    string             `bson:"outret,omitempty"`
	Quota     bool               `bson:"quota,omitempty"`
	Sleeper   bool               `bson:"sleeper,omitempty"`
	DateBands []FareRailDateBand `bson:"datebands,omitempty"`
}

type FareRailCalendar struct {
	CFMarker     string    `bson:"cfmarker,omitempty"`
	Type         string    `bson:"type"`
	RouteCode    string    `bson:"routecode,omitempty"`
	Country      string    `bson:"country,omitempty"`
	ValidFrom    time.Time `bson:"validfrom,omitempty"`
	ValidTo      time.Time `bson:"validto,omitempty"`
	FromMonthDay string    `bson:"frommonthday"`
	ToMonthDay   string    `bson:"tomonthday"`
	Days         string    `bson:"days,omitempty"`
}

type FareTransferRule struct {
	FareRecord      `bson:",inline"`
	FromLegGroupRef string   `bson:"fromleggroupref,omitempty"`
	ToLegGroupRef   string   `bson:"toleggroupref,omitempty"`
	ProductRef      string   `bson:"productref,omitempty"`
	ProductRefs     []string `bson:"productrefs,omitempty"`
	TransferCount   *int     `bson:"transfercount,omitempty"`
	DurationSeconds *int     `bson:"durationseconds,omitempty"`
	DurationType    string   `bson:"durationtype,omitempty"`
	TransferType    string   `bson:"transfertype,omitempty"`
}

func (rule FareTransferRule) Validate() error {
	if err := rule.FareRecord.Validate(); err != nil {
		return err
	}
	switch rule.TransferType {
	case "0", "1", "2":
	default:
		return fmt.Errorf("unsupported fare transfer type %q", rule.TransferType)
	}
	if rule.TransferCount != nil {
		if *rule.TransferCount == 0 || *rule.TransferCount < -1 {
			return fmt.Errorf("fare transfer count must be -1 or a positive integer")
		}
		if rule.FromLegGroupRef != rule.ToLegGroupRef {
			return fmt.Errorf("fare transfer count requires matching from and to leg groups")
		}
	} else if rule.FromLegGroupRef == rule.ToLegGroupRef {
		return fmt.Errorf("fare transfer count is required for matching from and to leg groups")
	}
	if rule.DurationSeconds != nil {
		if *rule.DurationSeconds <= 0 {
			return fmt.Errorf("fare transfer duration must be positive")
		}
		switch rule.DurationType {
		case "0", "1", "2", "3":
		default:
			return fmt.Errorf("unsupported fare transfer duration type %q", rule.DurationType)
		}
	} else if rule.DurationType != "" {
		return fmt.Errorf("fare transfer duration type requires a duration")
	}
	return nil
}

type FareEffectiveLegJoin struct {
	FareRecord       `bson:",inline"`
	FromNetworkRef   string `bson:"fromnetworkref,omitempty"`
	ToNetworkRef     string `bson:"tonetworkref,omitempty"`
	FromStopRef      string `bson:"fromstopref,omitempty"`
	ToStopRef        string `bson:"tostopref,omitempty"`
	FromTimeframeRef string `bson:"fromtimeframeref,omitempty"`
	ToTimeframeRef   string `bson:"totimeframeref,omitempty"`
}

type FareCap struct {
	FareRecord          `bson:",inline"`
	Name                string    `bson:"name"`
	Price               FareMoney `bson:"price"`
	Period              string    `bson:"period"`
	ResetTimezone       string    `bson:"resettimezone,omitempty"`
	ResetTime           string    `bson:"resettime,omitempty"`
	EligibleProductRefs []string  `bson:"eligibleproductrefs,omitempty"`
	EligibleMediaRefs   []string  `bson:"eligiblemediarefs,omitempty"`
	EligibleRiderRefs   []string  `bson:"eligibleriderrefs,omitempty"`
	LocationRefs        []string  `bson:"locationrefs,omitempty"`
}

// FareVersionHead is the sole mutable pointer in the fare catalogue. Imported
// records are written under a new version and become queryable only after this
// head is replaced.
type FareVersionHead struct {
	DatasetID                string    `bson:"datasetid"`
	ActiveVersionID          string    `bson:"activeversionid"`
	PreviousVersionID        string    `bson:"previousversionid,omitempty"`
	SourceSequence           string    `bson:"sourcesequence,omitempty"`
	LinkedScheduleDatasetID  string    `bson:"linkedscheduledatasetid,omitempty"`
	ActivatedAt              time.Time `bson:"activatedat"`
	ActivationFence          int64     `bson:"activationfence,omitempty"`
	LeaseOwner               string    `bson:"leaseowner,omitempty"`
	LeaseExpiresAt           time.Time `bson:"leaseexpiresat,omitempty"`
	StagingFareVersionID     string    `bson:"stagingfareversionid,omitempty"`
	PendingRetiredVersionIDs []string  `bson:"pendingretiredversionids,omitempty"`
	// StagingCleanupVersionIDs are durable tombstones for abandoned candidates.
	// They remain queued so a later lease can sweep writes that were already in flight.
	StagingCleanupVersionIDs []string `bson:"stagingcleanupversionids,omitempty"`
}

type FareCertainty string

const (
	FareCertaintyAuthoritative FareCertainty = "authoritative"
	FareCertaintyComputed      FareCertainty = "computed"
	FareCertaintyCatalogueOnly FareCertainty = "catalogue_only"
	FareCertaintyPartial       FareCertainty = "partial"
	FareCertaintyUnavailable   FareCertainty = "unavailable"
)

type FareQuoteAlternative struct {
	ProductRef  string    `json:"product_ref" groups:"basic,web-planner"`
	ProductRefs []string  `json:"product_refs,omitempty" groups:"basic,web-planner"`
	Name        string    `json:"name" groups:"basic,web-planner"`
	Price       FareMoney `json:"price" groups:"basic,web-planner"`
	Conditions  []string  `json:"conditions,omitempty" groups:"basic,web-planner"`
	PurchaseURL string    `json:"purchase_url,omitempty" groups:"basic,web-planner"`
}

type FareQuoteComponent struct {
	JourneyRef     string                 `json:"journey_ref,omitempty" groups:"basic,web-planner"`
	JourneyRefs    []string               `json:"journey_refs,omitempty" groups:"basic,web-planner"`
	ServiceRef     string                 `json:"service_ref,omitempty" groups:"basic,web-planner"`
	OriginStopRef  string                 `json:"origin_stop_ref,omitempty" groups:"basic,web-planner"`
	DestinationRef string                 `json:"destination_stop_ref,omitempty" groups:"basic,web-planner"`
	StartTime      time.Time              `json:"start_time,omitempty" groups:"basic,web-planner"`
	ArrivalTime    time.Time              `json:"arrival_time,omitempty" groups:"basic,web-planner"`
	Price          *FareMoney             `json:"price,omitempty" groups:"basic,web-planner"`
	Alternatives   []FareQuoteAlternative `json:"alternatives,omitempty" groups:"basic,web-planner"`
	Certainty      FareCertainty          `json:"certainty" groups:"basic,web-planner"`
	Reason         string                 `json:"reason,omitempty" groups:"basic,web-planner"`
}

type FareQuote struct {
	Certainty            FareCertainty          `json:"certainty" groups:"basic,web-planner"`
	Availability         FareAvailability       `json:"availability" groups:"basic,web-planner"`
	Price                *FareMoney             `json:"price,omitempty" groups:"basic,web-planner"`
	Alternatives         []FareQuoteAlternative `json:"alternatives,omitempty" groups:"basic,web-planner"`
	Components           []FareQuoteComponent   `json:"components,omitempty" groups:"basic,web-planner"`
	PassengerAssumptions []string               `json:"passenger_assumptions,omitempty" groups:"basic,web-planner"`
	Source               string                 `json:"source,omitempty" groups:"basic,web-planner"`
	Reason               string                 `json:"reason,omitempty" groups:"basic,web-planner"`
}

// FareCatalogue is a candidate or active version loaded as one validation unit.
// Persisted records remain in their typed collections.
type FareCatalogue struct {
	DatasetID       string
	FareVersionID   string
	Locations       []FareLocation
	Tariffs         []FareTariff
	Products        []FareProduct
	RiderCategories []FareRiderCategory
	Media           []FareMedia
	Networks        []FareNetwork
	RouteNetworks   []FareRouteNetwork
	Timeframes      []FareTimeframe
	Offers          []FareOffer
	LegRules        []FareLegRule
	TransferRules   []FareTransferRule
	EffectiveJoins  []FareEffectiveLegJoin
	Caps            []FareCap
}

func (record FareRecord) Validate() error {
	if record.PrimaryIdentifier == "" || record.DatasetID == "" || record.FareVersionID == "" {
		return fmt.Errorf("fare record identity, dataset and version are required")
	}
	if !record.EffectiveFrom.IsZero() && !record.EffectiveTo.IsZero() && record.EffectiveTo.Before(record.EffectiveFrom) {
		return fmt.Errorf("fare effective end precedes its start")
	}
	return nil
}

func (location FareLocation) Validate() error {
	if err := location.FareRecord.Validate(); err != nil {
		return err
	}
	switch location.Type {
	case FareLocationTypeStop, FareLocationTypeZone, FareLocationTypeArea, FareLocationTypeGroup, FareLocationTypeStage:
	default:
		return fmt.Errorf("unsupported fare location type %q", location.Type)
	}
	return nil
}

func (tariff FareTariff) Validate() error {
	if err := tariff.FareRecord.Validate(); err != nil {
		return err
	}
	if !tariff.SalesValidFrom.IsZero() && !tariff.SalesValidTo.IsZero() && tariff.SalesValidTo.Before(tariff.SalesValidFrom) {
		return fmt.Errorf("fare tariff sales end precedes its start")
	}
	if !tariff.UsageValidFrom.IsZero() && !tariff.UsageValidTo.IsZero() && tariff.UsageValidTo.Before(tariff.UsageValidFrom) {
		return fmt.Errorf("fare tariff usage end precedes its start")
	}
	if tariff.UsageValiditySec < 0 {
		return fmt.Errorf("fare tariff usage validity cannot be negative")
	}
	if tariff.Currency != "" {
		if err := (FareMoney{Currency: tariff.Currency}).Validate(); err != nil {
			return fmt.Errorf("fare tariff currency: %w", err)
		}
	}
	return nil
}

func (product FareProduct) Validate() error {
	if err := product.FareRecord.Validate(); err != nil {
		return err
	}
	if product.Name == "" || product.Type == "" {
		return fmt.Errorf("fare product name and type are required")
	}
	if product.MaxTransfers != nil && *product.MaxTransfers < 0 {
		return fmt.Errorf("fare product transfer count cannot be negative")
	}
	if product.TransferDurationSeconds != nil && *product.TransferDurationSeconds < 0 {
		return fmt.Errorf("fare product transfer duration cannot be negative")
	}
	if product.TransfersUnlimited && product.MaxTransfers != nil {
		return fmt.Errorf("fare product cannot set both a transfer limit and unlimited transfers")
	}
	if product.RailTicket != nil {
		if product.RailTicket.MinimumPassengers < 0 || product.RailTicket.MaximumPassengers < 0 {
			return fmt.Errorf("rail ticket passenger limits cannot be negative")
		}
		if product.RailTicket.MinimumPassengers > 0 && product.RailTicket.MaximumPassengers > 0 && product.RailTicket.MaximumPassengers < product.RailTicket.MinimumPassengers {
			return fmt.Errorf("rail ticket maximum passengers is below its minimum")
		}
		switch product.RailTicket.Reservation {
		case "", "N", "O", "R", "B", "E":
		default:
			return fmt.Errorf("unsupported rail ticket reservation marker %q", product.RailTicket.Reservation)
		}
	}
	switch product.Type {
	case FareProductTypeSingleRide, FareProductTypeReturn, FareProductTypePass, FareProductTypeSeason, FareProductTypeMultiTrip, FareProductTypeOther:
	default:
		return fmt.Errorf("unsupported fare product type %q", product.Type)
	}
	return nil
}

func (offer FareOffer) Validate() error {
	if err := offer.FareRecord.Validate(); err != nil {
		return err
	}
	if offer.ProductRef == "" {
		return fmt.Errorf("fare offer product reference is required")
	}
	if offer.Availability != FareAvailabilityUnknown && offer.Availability != FareAvailabilityAvailable && offer.Availability != FareAvailabilityUnavailable {
		return fmt.Errorf("unsupported fare availability %q", offer.Availability)
	}
	if err := offer.Price.Validate(); err != nil {
		return err
	}
	if !offer.SalesValidFrom.IsZero() && !offer.SalesValidTo.IsZero() && offer.SalesValidTo.Before(offer.SalesValidFrom) {
		return fmt.Errorf("fare offer sales end precedes its start")
	}
	return nil
}

func (rule FareLegRule) Validate() error {
	if err := rule.FareRecord.Validate(); err != nil {
		return err
	}
	if len(rule.ProductRefs) == 0 && (rule.ProductRef == "" || rule.OfferRef == "") {
		return fmt.Errorf("fare leg rule product and offer references are required")
	}
	if len(rule.ProductRefs) != len(rule.OfferRefs) {
		return fmt.Errorf("fare leg rule product and offer variants must align")
	}
	if rule.RulePriority < 0 {
		return fmt.Errorf("fare leg rule priority cannot be negative")
	}
	if rule.RailConditions != nil {
		if err := rule.RailConditions.Validate(); err != nil {
			return fmt.Errorf("fare rail conditions: %w", err)
		}
	}
	return nil
}

func (conditions FareRailConditions) Validate() error {
	if conditions.Route != nil {
		for _, location := range conditions.Route.Locations {
			if location.IncludeExclude != "I" && location.IncludeExclude != "E" {
				return fmt.Errorf("rail route has unsupported include/exclude marker %q", location.IncludeExclude)
			}
			if location.NLC == "" && location.CRS == "" {
				return fmt.Errorf("rail route location has no NLC or CRS code")
			}
		}
	}
	for _, restriction := range conditions.Restrictions {
		if restriction.Code == "" {
			return fmt.Errorf("rail restriction code is required")
		}
		if !restriction.ValidFrom.IsZero() && !restriction.ValidTo.IsZero() && restriction.ValidTo.Before(restriction.ValidFrom) {
			return fmt.Errorf("rail restriction %s ends before it starts", restriction.Code)
		}
		if restriction.OutwardMode != "" && restriction.OutwardMode != "P" && restriction.OutwardMode != "N" {
			return fmt.Errorf("rail restriction %s has unsupported outward mode %q", restriction.Code, restriction.OutwardMode)
		}
		if restriction.ReturnMode != "" && restriction.ReturnMode != "P" && restriction.ReturnMode != "N" {
			return fmt.Errorf("rail restriction %s has unsupported return mode %q", restriction.Code, restriction.ReturnMode)
		}
		for _, band := range restriction.DateBands {
			if err := band.Validate(); err != nil {
				return fmt.Errorf("rail restriction %s date band: %w", restriction.Code, err)
			}
		}
		for _, timeRestriction := range restriction.TimeRestrictions {
			if timeRestriction.Sequence == "" || (timeRestriction.OutRet != "O" && timeRestriction.OutRet != "R") {
				return fmt.Errorf("rail restriction %s has an invalid time restriction identity", restriction.Code)
			}
			if timeRestriction.TimeFrom < 0 || timeRestriction.TimeFrom >= 24*60 || timeRestriction.TimeTo < 0 || timeRestriction.TimeTo >= 24*60 {
				return fmt.Errorf("rail restriction %s has an invalid time window", restriction.Code)
			}
			switch timeRestriction.Event {
			case "A", "D", "V":
			default:
				return fmt.Errorf("rail restriction %s has unsupported event %q", restriction.Code, timeRestriction.Event)
			}
			for _, band := range timeRestriction.DateBands {
				if err := band.Validate(); err != nil {
					return fmt.Errorf("rail restriction %s time date band: %w", restriction.Code, err)
				}
			}
		}
		for _, trainRestriction := range restriction.TrainRestrictions {
			if trainRestriction.TrainUID == "" || (trainRestriction.OutRet != "O" && trainRestriction.OutRet != "R") {
				return fmt.Errorf("rail restriction %s has an invalid train restriction identity", restriction.Code)
			}
			for _, band := range trainRestriction.DateBands {
				if err := band.Validate(); err != nil {
					return fmt.Errorf("rail restriction %s train date band: %w", restriction.Code, err)
				}
			}
		}
	}
	for _, calendar := range conditions.TicketCalendars {
		switch calendar.Type {
		case "I", "D", "S":
		default:
			return fmt.Errorf("ticket calendar has unsupported type %q", calendar.Type)
		}
		if !calendar.ValidFrom.IsZero() && !calendar.ValidTo.IsZero() && calendar.ValidTo.Before(calendar.ValidFrom) {
			return fmt.Errorf("ticket calendar ends before it starts")
		}
		if err := (FareRailDateBand{FromMonthDay: calendar.FromMonthDay, ToMonthDay: calendar.ToMonthDay, Days: calendar.Days}).Validate(); err != nil {
			return fmt.Errorf("ticket calendar date band: %w", err)
		}
	}
	return nil
}

func (band FareRailDateBand) Validate() error {
	if len(band.FromMonthDay) != 4 || len(band.ToMonthDay) != 4 {
		return fmt.Errorf("month/day dates must use MMDD")
	}
	if _, err := time.ParseInLocation("0102", band.FromMonthDay, time.UTC); err != nil {
		return fmt.Errorf("invalid date-band start %q", band.FromMonthDay)
	}
	if _, err := time.ParseInLocation("0102", band.ToMonthDay, time.UTC); err != nil {
		return fmt.Errorf("invalid date-band end %q", band.ToMonthDay)
	}
	if band.Days != "" {
		if len(band.Days) != 7 {
			return fmt.Errorf("weekdays must contain seven markers")
		}
		for _, day := range band.Days {
			if day != 'Y' && day != 'N' {
				return fmt.Errorf("invalid weekday marker %q", day)
			}
		}
	}
	return nil
}

func (catalogue FareCatalogue) Validate() error {
	if catalogue.DatasetID == "" || catalogue.FareVersionID == "" {
		return fmt.Errorf("fare catalogue dataset and version are required")
	}
	checkRecord := func(kind string, record FareRecord) error {
		if err := record.Validate(); err != nil {
			return fmt.Errorf("%s %q: %w", kind, record.PrimaryIdentifier, err)
		}
		if record.DatasetID != catalogue.DatasetID || record.FareVersionID != catalogue.FareVersionID {
			return fmt.Errorf("%s %q belongs to another dataset or fare version", kind, record.PrimaryIdentifier)
		}
		return nil
	}

	locationIDs := map[string]struct{}{}
	for _, value := range catalogue.Locations {
		if err := checkRecord("fare location", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare location %q: %w", value.PrimaryIdentifier, err)
		}
		locationIDs[value.PrimaryIdentifier] = struct{}{}
	}
	for _, value := range catalogue.Locations {
		if value.ParentRef != "" {
			if _, exists := locationIDs[value.ParentRef]; !exists {
				return fmt.Errorf("fare location %q references missing parent %q", value.PrimaryIdentifier, value.ParentRef)
			}
		}
	}
	tariffIDs := map[string]struct{}{}
	for _, value := range catalogue.Tariffs {
		if err := checkRecord("fare tariff", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare tariff %q: %w", value.PrimaryIdentifier, err)
		}
		tariffIDs[value.PrimaryIdentifier] = struct{}{}
	}
	riderIDs := map[string]struct{}{}
	for _, value := range catalogue.RiderCategories {
		if err := checkRecord("fare rider category", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare rider category %q: %w", value.PrimaryIdentifier, err)
		}
		riderIDs[value.PrimaryIdentifier] = struct{}{}
	}
	mediaIDs := map[string]struct{}{}
	for _, value := range catalogue.Media {
		if err := checkRecord("fare media", value.FareRecord); err != nil {
			return err
		}
		switch value.Type {
		case FareMediaTypeNone, FareMediaTypePaper, FareMediaTypeTransitCard, FareMediaTypeApp, FareMediaTypeContactless, FareMediaTypeOther:
		default:
			return fmt.Errorf("fare media %q has unsupported type %q", value.PrimaryIdentifier, value.Type)
		}
		mediaIDs[value.PrimaryIdentifier] = struct{}{}
	}
	networkIDs := map[string]struct{}{}
	for _, value := range catalogue.Networks {
		if err := checkRecord("fare network", value.FareRecord); err != nil {
			return err
		}
		networkIDs[value.PrimaryIdentifier] = struct{}{}
	}
	timeframeGroups := map[string]struct{}{}
	for _, value := range catalogue.Timeframes {
		if err := checkRecord("fare timeframe", value.FareRecord); err != nil {
			return err
		}
		if value.GroupRef == "" || value.StartTimeSecond < 0 || value.EndTimeSecond <= value.StartTimeSecond {
			return fmt.Errorf("fare timeframe %q has an invalid group or time range", value.PrimaryIdentifier)
		}
		timeframeGroups[value.GroupRef] = struct{}{}
	}
	productIDs := map[string]struct{}{}
	for _, value := range catalogue.Products {
		if err := checkRecord("fare product", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare product %q: %w", value.PrimaryIdentifier, err)
		}
		if value.RiderRef != "" {
			if _, exists := riderIDs[value.RiderRef]; !exists {
				return fmt.Errorf("fare product %q references missing rider category %q", value.PrimaryIdentifier, value.RiderRef)
			}
		}
		if value.MediaRef != "" {
			if _, exists := mediaIDs[value.MediaRef]; !exists {
				return fmt.Errorf("fare product %q references missing media %q", value.PrimaryIdentifier, value.MediaRef)
			}
		}
		productIDs[value.PrimaryIdentifier] = struct{}{}
	}
	offerIDs := map[string]struct{}{}
	for _, value := range catalogue.Offers {
		if err := checkRecord("fare offer", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare offer %q: %w", value.PrimaryIdentifier, err)
		}
		if _, exists := productIDs[value.ProductRef]; !exists {
			return fmt.Errorf("fare offer %q references missing product %q", value.PrimaryIdentifier, value.ProductRef)
		}
		if value.TariffRef != "" {
			if _, exists := tariffIDs[value.TariffRef]; !exists {
				return fmt.Errorf("fare offer %q references missing tariff %q", value.PrimaryIdentifier, value.TariffRef)
			}
		}
		offerIDs[value.PrimaryIdentifier] = struct{}{}
	}
	legGroups := map[string]struct{}{}
	for _, value := range catalogue.LegRules {
		if err := checkRecord("fare leg rule", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare leg rule %q: %w", value.PrimaryIdentifier, err)
		}
		productRefs, offerRefs := value.ProductRefs, value.OfferRefs
		if len(productRefs) == 0 {
			productRefs = []string{value.ProductRef}
			offerRefs = []string{value.OfferRef}
		}
		for index, productRef := range productRefs {
			if _, exists := productIDs[productRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing product %q", value.PrimaryIdentifier, productRef)
			}
			if _, exists := offerIDs[offerRefs[index]]; !exists {
				return fmt.Errorf("fare leg rule %q references missing offer %q", value.PrimaryIdentifier, offerRefs[index])
			}
		}
		if value.RiderRef != "" {
			if _, exists := riderIDs[value.RiderRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing rider category %q", value.PrimaryIdentifier, value.RiderRef)
			}
		}
		if value.MediaRef != "" {
			if _, exists := mediaIDs[value.MediaRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing media %q", value.PrimaryIdentifier, value.MediaRef)
			}
		}
		for _, locationRef := range append(append([]string(nil), value.ViaRefs...), value.OriginRef, value.DestinationRef) {
			if locationRef != "" {
				if _, exists := locationIDs[locationRef]; !exists {
					return fmt.Errorf("fare leg rule %q references missing location %q", value.PrimaryIdentifier, locationRef)
				}
			}
		}
		if value.NetworkRef != "" {
			if _, exists := networkIDs[value.NetworkRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing network %q", value.PrimaryIdentifier, value.NetworkRef)
			}
		}
		for _, timeframeRef := range []string{value.FromTimeframeRef, value.ToTimeframeRef} {
			if timeframeRef != "" {
				if _, exists := timeframeGroups[timeframeRef]; !exists {
					return fmt.Errorf("fare leg rule %q references missing timeframe group %q", value.PrimaryIdentifier, timeframeRef)
				}
			}
		}
		if value.LegGroupRef != "" {
			legGroups[value.LegGroupRef] = struct{}{}
		}
	}
	for _, value := range catalogue.RouteNetworks {
		if err := checkRecord("fare route network", value.FareRecord); err != nil {
			return err
		}
		if _, exists := networkIDs[value.NetworkRef]; !exists {
			return fmt.Errorf("fare route network %q references missing network %q", value.PrimaryIdentifier, value.NetworkRef)
		}
	}
	for _, value := range catalogue.EffectiveJoins {
		if err := checkRecord("fare effective-leg join", value.FareRecord); err != nil {
			return err
		}
		for _, networkRef := range []string{value.FromNetworkRef, value.ToNetworkRef} {
			if networkRef != "" {
				if _, exists := networkIDs[networkRef]; !exists {
					return fmt.Errorf("fare effective-leg join %q references missing network %q", value.PrimaryIdentifier, networkRef)
				}
			}
		}
		for _, timeframeRef := range []string{value.FromTimeframeRef, value.ToTimeframeRef} {
			if timeframeRef != "" {
				if _, exists := timeframeGroups[timeframeRef]; !exists {
					return fmt.Errorf("fare effective-leg join %q references missing timeframe group %q", value.PrimaryIdentifier, timeframeRef)
				}
			}
		}
	}
	for _, value := range catalogue.TransferRules {
		if err := checkRecord("fare transfer rule", value.FareRecord); err != nil {
			return err
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("fare transfer rule %q: %w", value.PrimaryIdentifier, err)
		}
		for _, groupRef := range []string{value.FromLegGroupRef, value.ToLegGroupRef} {
			if groupRef != "" {
				if _, exists := legGroups[groupRef]; !exists {
					return fmt.Errorf("fare transfer rule %q references missing leg group %q", value.PrimaryIdentifier, groupRef)
				}
			}
		}
		productRefs := value.ProductRefs
		if len(productRefs) == 0 && value.ProductRef != "" {
			productRefs = []string{value.ProductRef}
		}
		for _, productRef := range productRefs {
			if _, exists := productIDs[productRef]; !exists {
				return fmt.Errorf("fare transfer rule %q references missing product %q", value.PrimaryIdentifier, productRef)
			}
		}
	}
	for _, value := range catalogue.Caps {
		if err := checkRecord("fare cap", value.FareRecord); err != nil {
			return err
		}
		if err := value.Price.ValidateNonNegative(); err != nil {
			return fmt.Errorf("fare cap %q: %w", value.PrimaryIdentifier, err)
		}
		for _, productRef := range value.EligibleProductRefs {
			if _, exists := productIDs[productRef]; !exists {
				return fmt.Errorf("fare cap %q references missing product %q", value.PrimaryIdentifier, productRef)
			}
		}
		for _, mediaRef := range value.EligibleMediaRefs {
			if _, exists := mediaIDs[mediaRef]; !exists {
				return fmt.Errorf("fare cap %q references missing media %q", value.PrimaryIdentifier, mediaRef)
			}
		}
		for _, riderRef := range value.EligibleRiderRefs {
			if _, exists := riderIDs[riderRef]; !exists {
				return fmt.Errorf("fare cap %q references missing rider category %q", value.PrimaryIdentifier, riderRef)
			}
		}
		for _, locationRef := range value.LocationRefs {
			if _, exists := locationIDs[locationRef]; !exists {
				return fmt.Errorf("fare cap %q references missing location %q", value.PrimaryIdentifier, locationRef)
			}
		}
	}
	return nil
}

// ValidateFareCatalogue rejects internal links that would otherwise make a
// candidate fare version appear complete while producing ungrounded quotes.
func ValidateFareCatalogue(locations []FareLocation, products []FareProduct, offers []FareOffer, rules []FareLegRule) error {
	locationIDs := make(map[string]struct{}, len(locations))
	for _, location := range locations {
		if err := location.Validate(); err != nil {
			return fmt.Errorf("fare location %q: %w", location.PrimaryIdentifier, err)
		}
		locationIDs[location.PrimaryIdentifier] = struct{}{}
	}
	productIDs := make(map[string]struct{}, len(products))
	for _, product := range products {
		if err := product.Validate(); err != nil {
			return fmt.Errorf("fare product %q: %w", product.PrimaryIdentifier, err)
		}
		productIDs[product.PrimaryIdentifier] = struct{}{}
	}
	offerIDs := make(map[string]struct{}, len(offers))
	for _, offer := range offers {
		if err := offer.Validate(); err != nil {
			return fmt.Errorf("fare offer %q: %w", offer.PrimaryIdentifier, err)
		}
		if _, exists := productIDs[offer.ProductRef]; !exists {
			return fmt.Errorf("fare offer %q references missing product %q", offer.PrimaryIdentifier, offer.ProductRef)
		}
		offerIDs[offer.PrimaryIdentifier] = struct{}{}
	}
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("fare leg rule %q: %w", rule.PrimaryIdentifier, err)
		}
		productRefs, offerRefs := rule.ProductRefs, rule.OfferRefs
		if len(productRefs) == 0 {
			productRefs = []string{rule.ProductRef}
			offerRefs = []string{rule.OfferRef}
		}
		for index, productRef := range productRefs {
			if _, exists := productIDs[productRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing product %q", rule.PrimaryIdentifier, productRef)
			}
			if _, exists := offerIDs[offerRefs[index]]; !exists {
				return fmt.Errorf("fare leg rule %q references missing offer %q", rule.PrimaryIdentifier, offerRefs[index])
			}
		}
		for _, locationRef := range append(append([]string(nil), rule.ViaRefs...), rule.OriginRef, rule.DestinationRef) {
			if locationRef == "" {
				continue
			}
			if _, exists := locationIDs[locationRef]; !exists {
				return fmt.Errorf("fare leg rule %q references missing location %q", rule.PrimaryIdentifier, locationRef)
			}
		}
	}
	return nil
}
