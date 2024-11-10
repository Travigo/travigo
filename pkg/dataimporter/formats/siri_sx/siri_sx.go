package siri_sx

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/adjust/rmq/v5"
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
	if len(situationElement.Consequence) > 0 && situationElement.Consequence[0].Severity == "severe" {
		alertType = ctdf.ServiceAlertTypeWarning
	}

	var identifyingInformation []map[string]string
	for _, consequence := range situationElement.Consequence {
		for _, network := range consequence.AffectedNetworks {
			for _, line := range network.AffectedLine {
				identifyingInformation = append(identifyingInformation, map[string]string{
					"LineRef":         line.LineRef,
					"PubishedLineRef": line.PublishedLineRef,
					"OperatorRef":     line.OperatorRef,
					"LinkedDataset":   "gb-dft-bods-gtfs-schedule", // not always going to be true in future
				})
			}
		}

		for _, stopPoint := range consequence.AffectedStopPoints {
			identifyingInformation = append(identifyingInformation, map[string]string{
				"StopPointRef":  stopPoint.StopPointRef,
				"LinkedDataset": "gb-dft-naptan", // not always going to be true in future
			})
		}
	}

	title := situationElement.Summary
	description := situationElement.Description

	hash := sha256.New()
	hash.Write([]byte(alertType))
	hash.Write([]byte(title))
	hash.Write([]byte(description))
	localIDhash := fmt.Sprintf("%x", hash.Sum(nil))

	updateEvent := vehicletracker.VehicleUpdateEvent{
		MessageType: vehicletracker.VehicleUpdateEventTypeServiceAlert,
		LocalID:     fmt.Sprintf("%s-servicealert-%d-%d-%s", dataset.Identifier, validityPeriodStart.UnixMicro(), validityPeriodEnd.UnixMicro(), localIDhash),

		ServiceAlertUpdate: &vehicletracker.ServiceAlertUpdate{
			Type:        alertType,
			Title:       title,
			Description: description,
			ValidFrom:   validityPeriodStart,
			ValidUntil:  validityPeriodEnd,

			IdentifyingInformation: identifyingInformation,
		},

		SourceType: "siri-sx",
		DataSource: datasource,
		RecordedAt: versionedAtTime,
	}

	updateEventJson, _ := json.Marshal(updateEvent)
	queue.PublishBytes(updateEventJson)

	return true
}
