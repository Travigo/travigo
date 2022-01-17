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
			"Christmas Day":          "GB:BankHoliday:ChristmasDay",
			"Boxing Day":             "GB:BankHoliday:BoxingDay",
			"New Year’s Day":         "GB:BankHoliday:NewYearsDay",
			"Good Friday":            "GB:BankHoliday:GoodFriday",
			"Easter Monday":          "GB:BankHoliday:EasterMonday",
			"Early May bank holiday": "GB:BankHoliday:MayDay",
			"Spring bank holiday":    "GB:BankHoliday:SpringBank",
			"Summer bank holiday":    "GB:BankHoliday:LateSummerBankHolidayNotScotland",
			"St Andrew's Day":        "GB:BankHoliday:StAndrewsDay",
			"2nd January":            "GB:BankHoliday:Jan2ndScotland",
		}

		if specialDayMapping[event.Title] != "" {
			if SpecialDays[eventDate.Year()] == nil {
				SpecialDays[eventDate.Year()] = make(map[string]time.Time)
			}

			SpecialDays[eventDate.Year()][specialDayMapping[event.Title]] = eventDate
		}
	}

	// Make them up for christmas and new years eve
	for year, yearMap := range SpecialDays {
		yearMap["GB:BankHoliday:ChristmasEve"] = time.Date(year, 12, 24, 0, 0, 0, 0, time.UTC)
		yearMap["GB:BankHoliday:NewYearsEve"] = time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	}
}
