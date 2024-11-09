package siri_sx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"golang.org/x/net/html/charset"
)

type SiriSX struct {
	reader io.Reader
	queue  rmq.Queue
}

func (s *SiriSX) SetupRealtimeQueue(queue rmq.Queue) {
	s.queue = queue
}

func (s *SiriSX) ParseFile(reader io.Reader) error {
	s.reader = reader

	return nil
}

func (s *SiriSX) Import(dataset datasets.DataSet, datasource *ctdf.DataSource) error {
	if !dataset.SupportedObjects.ServiceAlerts {
		return errors.New("This format requires servicealerts to be enabled")
	}

	var retrievedRecords int64
	var submittedRecords int64

	d := xml.NewDecoder(s.reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "PtSituationElement" {
				var situationElement SituationElement

				if err = d.DecodeElement(&situationElement, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					retrievedRecords += 1

					successfullyPublished := SubmitToProcessQueue(s.queue, &situationElement, dataset, datasource)

					if successfullyPublished {
						submittedRecords += 1
					}
				}
			}
		}
	}

	log.Info().Int64("retrieved", retrievedRecords).Int64("submitted", submittedRecords).Msgf("Parsed latest Siri-VM response")

	return nil
}

func SubmitToProcessQueue(queue rmq.Queue, situationElement *SituationElement, dataset datasets.DataSet, datasource *ctdf.DataSource) bool {
	datasource.OriginalFormat = "siri-sx"

	currentTime := time.Now()

	validityPeriodStart, _ := time.Parse(time.RFC3339, situationElement.ValidityPeriod.StartTime)
	validityPeriodEnd, err := time.Parse(time.RFC3339, situationElement.ValidityPeriod.EndTime)
	versionedAtTime, _ := time.Parse(time.RFC3339, situationElement.VersionedAtTime)

	if err != nil && validityPeriodEnd.Before(currentTime) {
		return false
	}

	var alertType ctdf.ServiceAlertType
	alertType = ctdf.ServiceAlertTypeInformation // TODO debug

	updateEvent := vehicletracker.VehicleUpdateEvent{
		MessageType: vehicletracker.VehicleUpdateEventTypeServiceAlert,
		LocalID:     fmt.Sprintf("%s-realtime-%d-%d", dataset.Identifier, validityPeriodStart.UnixMicro(), validityPeriodEnd.UnixMicro()),
		IdentifyingInformation: map[string]string{
			// "TripID":        tripID,
			// "RouteID":       routeID,
			// "StopID":        stopID,
			// "AgencyID":      agencyID,
			"LinkedDataset": dataset.LinkedDataset,
		},

		ServiceAlertUpdate: &vehicletracker.ServiceAlertUpdate{
			Type:        alertType,
			Title:       situationElement.Summary,
			Description: situationElement.Description,
			ValidFrom:   validityPeriodStart,
			ValidUntil:  validityPeriodEnd,
		},

		SourceType: "siri-sx",
		DataSource: datasource,
		RecordedAt: versionedAtTime,
	}

	pretty.Println(updateEvent)

	// updateEventJson, _ := json.Marshal(updateEvent)
	// queue.PublishBytes(updateEventJson)

	return true
}
