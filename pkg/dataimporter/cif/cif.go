package cif

import (
	"archive/zip"
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var suffixCheck = regexp.MustCompile(`^[2-9]+$`)
var stopTIPLOCCache = map[string]*ctdf.Stop{}

type CommonInterfaceFormat struct {
	TrainDefinitionSets []TrainDefinitionSet
	Associations        []Association

	PhysicalStations []PhysicalStation
	StationAliases   []StationAlias
}

type Association struct {
	TransactionType     string
	BaseUID             string
	AssocUID            string
	AssocStartDate      string
	AssocEndDate        string
	AssocDays           string
	AssocCat            string
	AssocDateInd        string
	AssocLocation       string
	BaseLocationSuffix  string
	AssocLocationSuffix string
	DiagramType         string
	AssociationType     string
	STPIndicator        string
}

func ParseCifBundle(source string) (CommonInterfaceFormat, error) {
	cifBundle := CommonInterfaceFormat{}

	archive, err := zip.OpenReader(source)
	if err != nil {
		log.Fatal().Str("source", source).Err(err).Msg("Could not open zip file")
	}
	defer archive.Close()

	for _, zipFile := range archive.File {

		file, err := zipFile.Open()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to open file")
		}
		defer file.Close()

		fileExtension := filepath.Ext(zipFile.Name)

		switch fileExtension {
		case ".MCA":
			log.Info().Str("file", zipFile.Name).Msgf("Parsing Full Basic Timetable Detail")
			cifBundle.ParseMCA(file)
			log.Info().
				Int("schedules", len(cifBundle.TrainDefinitionSets)).
				Int("associations", len(cifBundle.Associations)).
				Msgf("Parsed Full Basic Timetable Detail")
		case ".MSN":
			log.Info().Str("file", zipFile.Name).Msgf("Parsing Master Station Names")
			cifBundle.ParseMSN(file)
			log.Info().
				Int("stations", len(cifBundle.PhysicalStations)).
				Int("aliases", len(cifBundle.StationAliases)).
				Msgf("Parsed Master Station Names")
		}
	}

	return cifBundle, nil
}

func (c *CommonInterfaceFormat) ConvertToCTDF() []ctdf.Journey {
	var journeys []ctdf.Journey

	for _, trainDef := range c.TrainDefinitionSets {
		// Skip buses (WHY ARE THEY IN THE TRAIN SCHEDULE??)
		if trainDef.BasicSchedule.TrainStatus == "B" {
			continue
		}

		if trainDef.BasicSchedule.STPIndicator == "N" || trainDef.BasicSchedule.STPIndicator == "P" {
			journeyID := fmt.Sprintf("GB:RAIL:%s", trainDef.BasicSchedule.TrainUID)
			departureTime, _ := time.Parse("1504", trainDef.OriginLocation.PublicDepartureTime)

			// List of passenger stops
			// Start with the origin location
			passengerStops := []BasicLocation{
				{
					Location:               strings.TrimSpace(trainDef.OriginLocation.Location),
					ScheduledDepartureTime: trainDef.OriginLocation.ScheduledDepartureTime,
					PublicDepartureTime:    trainDef.OriginLocation.PublicDepartureTime,
				},
			}

			// Add all the intermediate stops that are actual passenger stations
			for _, location := range trainDef.IntermediateLocations {
				// No public arrival time? guess its not a real stop
				if location.PublicArrivalTime == "0000" {
					continue
				}

				tiploc := strings.TrimSpace(location.Location)

				// Get rid of the suffix from the tiploc
				if len(tiploc) == 8 && suffixCheck.MatchString(tiploc[7:8]) {
					tiploc = strings.TrimSpace(tiploc[0:7])
				}

				passengerStops = append(passengerStops, BasicLocation{
					Location: tiploc,

					ScheduledDepartureTime: location.ScheduledDepartureTime,
					PublicDepartureTime:    location.PublicDepartureTime,

					ScheduledArrivalTime: location.ScheduledArrivalTime,
					PublicArrivalTime:    location.PublicArrivalTime,
				})
			}

			// Add terminating location to passenger stops
			terminatingTiploc := strings.TrimSpace(trainDef.TerminatingLocation.Location)
			// Get rid of the suffix from the tiploc
			if len(terminatingTiploc) == 8 && suffixCheck.MatchString(terminatingTiploc[7:8]) {
				terminatingTiploc = strings.TrimSpace(terminatingTiploc[0:7])
			}
			passengerStops = append(passengerStops, BasicLocation{
				Location:             strings.TrimSpace(terminatingTiploc),
				ScheduledArrivalTime: trainDef.TerminatingLocation.ScheduledArrivalTime,
				PublicArrivalTime:    trainDef.TerminatingLocation.PublicArrivalTime,
			})

			// Generate a CTDF path from the passenger stops
			var path []*ctdf.JourneyPathItem

			for i := 1; i < len(passengerStops); i++ {
				originTIPLOC := passengerStops[i-1].Location
				originStop := getStopFromTIPLOC(originTIPLOC)

				destinationTIPLOC := passengerStops[i].Location
				destinationStop := getStopFromTIPLOC(destinationTIPLOC)

				if originStop == nil {
					log.Error().Str("tiploc", originTIPLOC).Msg("Unknown stop")
					continue
				}
				if destinationStop == nil {
					log.Error().Str("tiploc", destinationTIPLOC).Msg("Unknown stop")
					continue
				}

				path = append(path, &ctdf.JourneyPathItem{
					OriginStop:    originStop,
					OriginStopRef: originStop.PrimaryIdentifier,

					DestinationStop:    destinationStop,
					DestinationStopRef: destinationStop.PrimaryIdentifier,
				})
			}

			journey := ctdf.Journey{
				PrimaryIdentifier: journeyID,
				OtherIdentifiers: map[string]string{
					"TrainUID":         trainDef.BasicSchedule.TrainUID,
					"TrainIdentity":    trainDef.BasicSchedule.TrainIdentity,
					"HeadCode":         trainDef.BasicSchedule.Headcode,
					"TrainServiceCode": trainDef.BasicSchedule.TrainServiceCode,
				},
				CreationDateTime:     time.Now(),
				ModificationDateTime: time.Now(),
				DataSource: &ctdf.DataSource{
					OriginalFormat: "CIF",
					Provider:       "National-Rail",
					Dataset:        "timetable",
					Identifier:     "",
				},
				ServiceRef:         "",
				Service:            nil,
				OperatorRef:        "",
				Operator:           nil,
				Direction:          "",
				DepartureTime:      departureTime,
				DestinationDisplay: "",
				Availability:       &ctdf.Availability{},
				Path:               path,
			}

			journeys = append(journeys, journey)
		}
	}

	return journeys
}

func getStopFromTIPLOC(tiploc string) *ctdf.Stop {
	cacheValue := stopTIPLOCCache[tiploc]

	if cacheValue != nil {
		return cacheValue
	}

	stopCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers.Tiploc": tiploc}).Decode(&stop)

	stopTIPLOCCache[tiploc] = stop

	return stop
}
