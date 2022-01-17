package ctdf

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

var SpecialDays map[int]map[string]time.Time = make(map[int]map[string]time.Time) // year specialday time

func LoadSpecialDayCache() {
	loadGBBankHolidayCache()
}

func loadGBBankHolidayCache() {
	// TODO: Handle substitute christmas days as they still wont be working
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

	aggregateEvents := append(bankHolidaysRaw.NorthernIreland.Events, bankHolidaysRaw.EnglandAndWales.Events...)

	for _, event := range aggregateEvents {
		eventDate, _ := time.Parse(YearMonthDayFormat, event.Date)

		specialDayMapping := map[string]string{
			"Christmas Day":          "GB:BankHoliday:ChristmasDayHoliday",
			"Boxing Day":             "GB:BankHoliday:BoxingDayHoliday",
			"New Year’s Day":         "GB:BankHoliday:NewYearsDayHoliday",
			"Good Friday":            "GB:BankHoliday:GoodFriday",
			"Easter Monday":          "GB:BankHoliday:EasterMonday",
			"Early May bank holiday": "GB:BankHoliday:MayDay",
			"Spring bank holiday":    "GB:BankHoliday:SpringBank",
			"Summer bank holiday":    "GB:BankHoliday:LateSummerBankHolidayNotScotland",
			"St Andrew's Day":        "GB:BankHoliday:StAndrewsDayHoliday",
			"2nd January":            "GB:BankHoliday:Jan2ndScotlandHoliday",
		}

		if specialDayMapping[event.Title] != "" {
			if SpecialDays[eventDate.Year()] == nil {
				SpecialDays[eventDate.Year()] = make(map[string]time.Time)
			}

			SpecialDays[eventDate.Year()][specialDayMapping[event.Title]] = eventDate
		}
	}

	// Hardcoded set days
	for year, yearMap := range SpecialDays {
		yearMap["GB:BankHoliday:ChristmasEve"] = time.Date(year, 12, 24, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:NewYearsEve"] = time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

		yearMap["GB:BankHoliday:ChristmasDay"] = time.Date(year, 12, 25, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:BoxingDay"] = time.Date(year, 12, 26, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:NewYearsDay"] = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:Jan2ndScotland"] = time.Date(year, 1, 2, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:StAndrewsDay"] = time.Date(year, 11, 30, 0, 0, 0, 0, time.UTC)
	}
}
