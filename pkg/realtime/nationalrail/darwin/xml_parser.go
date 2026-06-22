package darwin

import (
	"encoding/xml"
	"io"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/html/charset"
)

func ParseXMLFile(reader io.Reader) (PushPortData, error) {
	pushPortData := PushPortData{}

	d := xml.NewDecoder(reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return pushPortData, err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			switch ty.Name.Local {
			case "TS":
				var trainStatus TrainStatus

				if err = d.DecodeElement(&trainStatus, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.TrainStatuses = append(pushPortData.TrainStatuses, trainStatus)
				}
			case "schedule":
				var schedule Schedule

				if err = d.DecodeElement(&schedule, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.Schedules = append(pushPortData.Schedules, schedule)
				}
			case "formationLoading":
				var formationLoading FormationLoading

				if err = d.DecodeElement(&formationLoading, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.FormationLoadings = append(pushPortData.FormationLoadings, formationLoading)
				}
			case "OW":
				var stationMessage StationMessage

				if err = d.DecodeElement(&stationMessage, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.StationMessages = append(pushPortData.StationMessages, stationMessage)
				}
			case "trainAlert":
				var trainAlert TrainAlert

				if err = d.DecodeElement(&trainAlert, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.TrainAlerts = append(pushPortData.TrainAlerts, trainAlert)
				}
			case "scheduleFormations":
				var scheduleFormation ScheduleFormations

				if err = d.DecodeElement(&scheduleFormation, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					pushPortData.ScheduleFormations = append(pushPortData.ScheduleFormations, scheduleFormation)
				}
			}
		}
	}

	return pushPortData, nil
}
