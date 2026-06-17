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

func (s *SiriSX) Import(dataset datasets.DataSet, datasource *ctdf.DataSourceReference) error {
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

	// PERF(low-risk): fixed incorrect log message — this is the SIRI-SX parse path, not SIRI-VM.
	log.Info().Int64("retrieved", retrievedRecords).Int64("submitted", submittedRecords).Msgf("Parsed latest Siri-SX response")

	return nil
}

func SubmitToProcessQueue(queue rmq.Queue, situationElement *SituationElement, dataset datasets.DataSet, datasource *ctdf.DataSourceReference) bool {
	datasource.OriginalFormat = "siri-sx"

	currentTime := time.Now()

	validityPeriodStart, _ := time.Parse(time.RFC3339, situationElement.ValidityPeriod.StartTime)
	validityPeriodEnd, err := time.Parse(time.RFC3339, situationElement.ValidityPeriod.EndTime)
	versionedAtTime, _ := time.Parse(time.RFC3339, situationElement.VersionedAtTime)

	// PERF/NOTE(needs-review): this expiry guard looks logically suspect. When time.Parse fails
	// (err != nil), validityPeriodEnd is the zero time, so validityPeriodEnd.Before(currentTime)
	// is always true and the record is dropped — meaning any element with an unparseable EndTime
	// is silently discarded. Conversely, an element with a valid-but-past EndTime (err == nil)
	// is NOT dropped because the err != nil arm short-circuits. The intent was likely
	// "err == nil && validityPeriodEnd.Before(currentTime)" (drop genuinely expired alerts).
	// Behaviour left unchanged pending user decision.
	if err != nil && validityPeriodEnd.Before(currentTime) {
		return false
	}

	var alertType ctdf.ServiceAlertType
	alertType = ctdf.ServiceAlertTypeInformation // TODO debug
	if len(situationElement.Consequence) > 0 && situationElement.Consequence[0].Severity == "severe" {
		alertType = ctdf.ServiceAlertTypeWarning
	}

	// PERF(low-risk): pre-size identifyingInformation to the exact number of entries the append
	// loops below will produce (one per affected line plus one per affected stop point, across
	// all consequences/networks). This avoids the repeated slice re-allocations/growth that
	// append would otherwise incur. Output contents and ordering are unchanged.
	identifyingInformationCount := 0
	for _, consequence := range situationElement.Consequence {
		for _, network := range consequence.AffectedNetworks {
			identifyingInformationCount += len(network.AffectedLine)
		}
		identifyingInformationCount += len(consequence.AffectedStopPoints)
	}

	identifyingInformation := make([]map[string]string, 0, identifyingInformationCount)
	for _, consequence := range situationElement.Consequence {
		for _, network := range consequence.AffectedNetworks {
			for _, line := range network.AffectedLine {
				identifyingInformation = append(identifyingInformation, map[string]string{
					"LineRef":         line.LineRef,
					"PubishedLineRef": line.PublishedLineRef,
					"OperatorRef":     line.OperatorRef,
					"LinkedDataset":   "gb-dft-bods-gtfs-schedule*", // not always going to be true in future
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

	// PERF(high-risk, NOT APPLIED): a cheaper non-cryptographic hash (e.g. fnv/xxhash) would
	// reduce per-record CPU here, but the resulting hex digest is embedded in the alert's
	// LocalID. Changing the hash changes generated IDs, which alters downstream identity and
	// dedup behaviour — left unchanged.
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

	// PERF(low-risk): handle the previously-discarded Marshal error. On failure, log and skip
	// publishing rather than pushing empty/garbage bytes onto the queue. Returns false so it
	// isn't counted as submitted.
	updateEventJson, marshalErr := json.Marshal(updateEvent)
	if marshalErr != nil {
		log.Error().Err(marshalErr).Str("localID", updateEvent.LocalID).Msg("Failed to marshal SIRI-SX update event, skipping")
		return false
	}
	queue.PublishBytes(updateEventJson)

	return true
}
