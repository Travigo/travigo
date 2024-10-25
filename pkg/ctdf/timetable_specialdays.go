package ctdf

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/rs/zerolog/log"
)

var SpecialDays = make(map[int]map[string]time.Time) // year specialday time

func LoadSpecialDayCache() {
	loadGBBankHolidayCache()
}

func loadGBBankHolidayCache() {
	nonAlphanumericRegex := regexp.MustCompile(`[^a-zA-Z0-9]+`)

	type BankHolidayEventsSchema struct {
		Title string
		Date  string
	}
	type BankHolidayCountrySchema struct {
		Division string
		Events   []BankHolidayEventsSchema
	}
	type BankHolidaySchema struct {
		EnglandAndWales BankHolidayCountrySchema `json:"england-and-wales"`
		Scotland        BankHolidayCountrySchema `json:"scotland"`
		NorthernIreland BankHolidayCountrySchema `json:"northern-ireland"`
	}

	resp, err := http.Get("https://www.gov.uk/bank-holidays.json")

	if err != nil {
		log.Error().Err(err).Msg("Failed to get Bank Holidays")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error().Err(err).Msg("Failed to get Bank Holidays")
		return
	}

	var bankHolidaysRaw BankHolidaySchema
	err = json.NewDecoder(resp.Body).Decode(&bankHolidaysRaw)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get Bank Holidays")
		return
	}

	aggregateEvents := append(bankHolidaysRaw.NorthernIreland.Events, bankHolidaysRaw.Scotland.Events...)
	aggregateEvents = append(aggregateEvents, bankHolidaysRaw.EnglandAndWales.Events...)

	for _, event := range aggregateEvents {
		eventDate, _ := time.Parse(YearMonthDayFormat, event.Date)

		specialDayMapping := map[string]string{
			"Christmas Day":          "gb-bankholiday-ChristmasDayHoliday",
			"Boxing Day":             "gb-bankholiday-BoxingDayHoliday",
			"New Year’s Day":         "gb-bankholiday-NewYearsDayHoliday",
			"Good Friday":            "gb-bankholiday-GoodFriday",
			"Easter Monday":          "gb-bankholiday-EasterMonday",
			"Early May bank holiday": "gb-bankholiday-MayDay",
			"Spring bank holiday":    "gb-bankholiday-SpringBank",
			"Summer bank holiday":    "gb-bankholiday-LateSummerBankHolidayNotScotland",
			"St Andrew's Day":        "gb-bankholiday-StAndrewsDayHoliday",
			"St Andrew’s Day":        "gb-bankholiday-StAndrewsDayHoliday",
			"2nd January":            "gb-bankholiday-Jan2ndScotlandHoliday",
		}

		eventID := specialDayMapping[event.Title]

		if eventID == "" {
			basicTitle := nonAlphanumericRegex.ReplaceAllString(event.Title, "")
			eventID = fmt.Sprintf("gb-unknownbankholiday-%s", basicTitle)
		}

		if SpecialDays[eventDate.Year()] == nil {
			SpecialDays[eventDate.Year()] = make(map[string]time.Time)
		}

		SpecialDays[eventDate.Year()][eventID] = eventDate
	}

	// Hardcoded set days
	for year, yearMap := range SpecialDays {
		yearMap["gb-bankholiday-ChristmasEve"] = time.Date(year, 12, 24, 0, 0, 0, 0, time.UTC)
		yearMap["gb-bankholiday-NewYearsEve"] = time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

		yearMap["gb-bankholiday-ChristmasDay"] = time.Date(year, 12, 25, 0, 0, 0, 0, time.UTC)
		yearMap["gb-bankholiday-BoxingDay"] = time.Date(year, 12, 26, 0, 0, 0, 0, time.UTC)
		yearMap["gb-bankholiday-NewYearsDay"] = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		yearMap["gb-bankholiday-Jan2ndScotland"] = time.Date(year, 1, 2, 0, 0, 0, 0, time.UTC)
		yearMap["gb-bankholiday-StAndrewsDay"] = time.Date(year, 11, 30, 0, 0, 0, 0, time.UTC)
	}
}
