package linxconsist

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"time"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

func ProcessMessage(ctx context.Context, messageBytes []byte, toc string) error {
	message := PassengerTrainConsistMessage{}
	decoder := xml.NewDecoder(bytes.NewReader(messageBytes))
	if err := decoder.Decode(&message); err != nil {
		return fmt.Errorf("decode passenger train consist message: %w", err)
	}

	return ProcessPassengerTrainConsist(ctx, message, toc)
}

func ProcessPassengerTrainConsist(ctx context.Context, message PassengerTrainConsistMessage, toc string) error {
	now := time.Now()
	identifier := ParseTrainIdentifier(message)

	if identifier.TrainUID == "" || identifier.StartDate == "" {
		return fmt.Errorf("missing usable train identifier in LINX core %q", identifier.Core)
	}

	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "LinxPassengerTrainConsistXML",
		ProviderName:   "Network Rail",
		ProviderID:     "gb-networkrail",
		DatasetID:      "gb-networkrail-linx-s506",
		Timestamp:      now.String(),
	}

	realtimeJourneyID := fmt.Sprintf("gb-nationalrailrealtime-%s:%s", identifier.StartDate, identifier.TrainUID)
	realtimeJourney, _ := realtimestore.FindByIdentifier(ctx, realtimeJourneyID)
	realtimeJourneyCreated := false

	if realtimeJourney == nil {
		var err error
		realtimeJourney, err = createRealtimeJourney(ctx, realtimeJourneyID, identifier, datasource, now)
		if err != nil {
			return err
		}
		realtimeJourneyCreated = true
	}

	if realtimeJourney.OtherIdentifiers == nil {
		realtimeJourney.OtherIdentifiers = map[string]string{}
	}
	realtimeJourney.OtherIdentifiers["TrainUID"] = identifier.TrainUID
	realtimeJourney.OtherIdentifiers["LinxCore"] = identifier.Core
	realtimeJourney.OtherIdentifiers["LinxHeadcode"] = identifier.Headcode
	realtimeJourney.OtherIdentifiers["LinxWorkingTimetableHour"] = identifier.WorkingTimetableHour
	realtimeJourney.OtherIdentifiers["LinxResponsibleRU"] = message.ResponsibleRU
	realtimeJourney.OtherIdentifiers["LinxTOC"] = toc
	realtimeJourney.OtherIdentifiers["LinxConsistMessageIdentifier"] = message.MessageHeader.MessageReference.MessageIdentifier
	if identifier.OperationalTrainNumber != "" {
		realtimeJourney.OtherIdentifiers["OperationalTrainNumber"] = identifier.OperationalTrainNumber
	}

	if realtimeJourneyCreated {
		realtimeJourney.ModificationDateTime = now
		if realtimeJourney.DataSource == nil {
			realtimeJourney.DataSource = datasource
		}
		realtimeJourney.DataSource.Timestamp = datasource.Timestamp
		if err := realtimestore.SaveRealtimeJourney(ctx, realtimeJourney); err != nil {
			return err
		}
	} else if err := realtimestore.SaveRealtimeJourneyMappings(ctx, realtimeJourney); err != nil {
		return err
	}
	realtimeJourney.DetailedRailInformation.Carriages = BuildRailCarriages(message)
	realtimeJourney.DetailedRailInformation.TrainLength = len(realtimeJourney.DetailedRailInformation.Carriages)

	// Extract Vehicle IDs from the carriages and store them in the DetailedRailInformation
	mappedVehicleIDs := make(map[string]struct{})
	for _, carriage := range realtimeJourney.DetailedRailInformation.Carriages {
		mappedVehicleIDs[carriage.VehicleID] = struct{}{}
	}

	vehicleIDs := make([]string, 0, len(mappedVehicleIDs))
	for vehicleID := range mappedVehicleIDs {
		vehicleIDs = append(vehicleIDs, vehicleID)
	}

	realtimeJourney.DetailedRailInformation.VehicleIDs = vehicleIDs
	pretty.Println(vehicleIDs)

	if err := realtimestore.UpdateRailDetailedAllocation(ctx, realtimeJourney.PrimaryIdentifier, realtimeJourney.DetailedRailInformation); err != nil {
		return err
	}

	log.Info().
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Str("trainuid", identifier.TrainUID).
		Str("core", identifier.Core).
		Int("carriages", len(realtimeJourney.DetailedRailInformation.Carriages)).
		Msg("Updated LINX passenger train consist")

	return nil
}

func createRealtimeJourney(ctx context.Context, realtimeJourneyID string, identifier LinxTrainIdentifier, datasource *ctdf.DataSourceReference, now time.Time) (*ctdf.RealtimeJourney, error) {
	journeysCollection := database.GetCollection("journeys")
	cursor, err := journeysCollection.Find(ctx, bson.M{"otheridentifiers.TrainUID": identifier.TrainUID})
	if err != nil {
		return nil, fmt.Errorf("find journey by train uid: %w", err)
	}
	defer cursor.Close(ctx)

	journeyDate, err := time.Parse("2006-01-02", identifier.StartDate)
	if err != nil {
		return nil, fmt.Errorf("parse LINX start date %q: %w", identifier.StartDate, err)
	}

	var journey *ctdf.Journey
	journeyPotentials := 0
	for cursor.Next(ctx) {
		var potentialJourney *ctdf.Journey
		if err := cursor.Decode(&potentialJourney); err != nil {
			log.Error().Err(err).Msg("Failed to decode Journey")
			continue
		}
		journeyPotentials += 1

		if potentialJourney.Availability.MatchDate(journeyDate) {
			journey = potentialJourney
			break
		}
	}

	if journey == nil {
		return nil, fmt.Errorf("failed to find journey for train uid %s on %s from %d candidates", identifier.TrainUID, identifier.StartDate, journeyPotentials)
	}

	journey.GetService()

	return &ctdf.RealtimeJourney{
		PrimaryIdentifier:      realtimeJourneyID,
		OtherIdentifiers:       map[string]string{},
		TimeoutDurationMinutes: 181,
		ActivelyTracked:        false,
		CreationDateTime:       now,
		ModificationDateTime:   now,
		Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,
		DataSource:             datasource,
		Journey:                journey,
		JourneyRunDate:         journeyDate,
		Service:                journey.Service,
		Stops:                  map[string]*ctdf.RealtimeJourneyStops{},
	}, nil
}
