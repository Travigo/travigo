package transxchange

import (
	"errors"
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
)

type TransXChange struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	Operators              []Operator
	Routes                 []Route
	Services               []Service
	JourneyPatternSections []JourneyPatternSection
	RouteSections          []RouteSection
	VehicleJourneys        []VehicleJourney

	SchemaVersion string `xml:",attr"`
}

func (n *TransXChange) Validate() error {
	if n.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if n.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if n.SchemaVersion != "2.4" {
		return errors.New("SchemaVersion must be 2.4")
	}

	return nil
}

func (doc *TransXChange) ImportIntoMongoAsCTDF(datasource *ctdf.DataSource) {
	datasource.OriginalFormat = "transxchange"
	datasource.Identifier = doc.ModificationDateTime

	// Map the local operator references to globally unique operator codes based on NOC
	operatorLocalMapping := map[string]string{}

	for _, operator := range doc.Operators {
		operatorLocalMapping[operator.ID] = fmt.Sprintf(ctdf.OperatorIDFormat, operator.NationalOperatorCode)
	}

	// Create reference map for JourneyPatternSections
	journeyPatternSectionReferences := map[string]JourneyPatternSection{}
	for _, journeyPatternSection := range doc.JourneyPatternSections {
		journeyPatternSectionReferences[journeyPatternSection.ID] = journeyPatternSection
	}

	// Get CTDF services from TransXChange Services & Lines
	services := []*ctdf.Service{}

	journeyPatternReferences := map[string]map[string]JourneyPattern{}

	for _, txcService := range doc.Services {
		for _, txcLine := range txcService.Lines {
			operatorRef := operatorLocalMapping[txcService.RegisteredOperatorRef]

			serviceIdentifier := fmt.Sprintf("%s:%s:%s", operatorRef, txcService.ServiceCode, txcLine.ID)

			ctdfService := ctdf.Service{
				PrimaryIdentifier: serviceIdentifier,
				OtherIdentifiers: map[string]string{
					"ServiceCode": txcService.ServiceCode,
					"LineID":      txcLine.ID,
				},

				DataSource: datasource,

				ServiceName:          txcLine.LineName,
				CreationDateTime:     txcService.CreationDateTime,
				ModificationDateTime: txcService.ModificationDateTime,

				StartDate: txcService.StartDate,
				EndDate:   txcService.EndDate,

				OperatorRef: operatorRef,

				InboundDescription: &ctdf.ServiceDescription{
					Origin:      txcLine.InboundOrigin,
					Destination: txcLine.InboundDestination,
					Description: txcLine.InboundDescription,
				},
				OutboundDescription: &ctdf.ServiceDescription{
					Origin:      txcLine.OutboundOrigin,
					Destination: txcLine.OutboundDestination,
					Description: txcLine.OutboundDescription,
				},
			}

			// Add JourneyPatterns into reference map
			journeyPatternReferences[serviceIdentifier] = map[string]JourneyPattern{}
			for _, journeyPattern := range txcService.JourneyPatterns {
				journeyPatternReferences[serviceIdentifier][journeyPattern.ID] = journeyPattern
			}

			services = append(services, &ctdfService)
		}
	}
	pretty.Println(services)

	// Get CTDF Journeys from TransXChange VehicleJourneys
	journeys := []*ctdf.Journey{}
	for _, txcJourney := range doc.VehicleJourneys {
		operatorRef := operatorLocalMapping[txcJourney.OperatorRef]
		serviceRef := fmt.Sprintf("%s:%s:%s", operatorRef, txcJourney.ServiceRef, txcJourney.LineRef)

		journeyPattern := journeyPatternReferences[serviceRef][txcJourney.JourneyPatternRef]
		journeyPatternSection := journeyPatternSectionReferences[journeyPattern.JourneyPatternSectionRefs]

		departureTime, _ := time.Parse("15:04:05", txcJourney.DepartureTime)

		ctdfJourney := ctdf.Journey{
			PrimaryIdentifier: fmt.Sprintf("%s:%s", operatorRef, txcJourney.PrivateCode),
			OtherIdentifiers: map[string]string{
				"PrivateCode": txcJourney.PrivateCode,
				"JourneyCode": txcJourney.VehicleJourneyCode,
			},

			CreationDateTime:     txcJourney.CreationDateTime,
			ModificationDateTime: txcJourney.ModificationDateTime,

			DataSource: datasource,

			ServiceRef:         serviceRef,
			OperatorRef:        operatorRef,
			Direction:          txcJourney.Direction,
			DeperatureTime:     departureTime,
			DestinationDisplay: journeyPattern.DestinationDisplay,

			// Availability *Availability

			Path: []ctdf.JourneyPathItem{},
		}

		for _, vehicleJourneyTimingLink := range txcJourney.VehicleJourneyTimingLinks {
			journeyPatternTimingLink, _ := journeyPatternSection.GetTimingLink(vehicleJourneyTimingLink.JourneyPatternTimingLinkRef)

			pathItem := ctdf.JourneyPathItem{
				OriginStopRef:      journeyPatternTimingLink.From.StopPointRef,
				DestinationStopRef: journeyPatternTimingLink.To.StopPointRef,

				EstimatedTravelTime: vehicleJourneyTimingLink.RunTime, // also need to handle wait times
				Distance:            0,
			}

			ctdfJourney.Path = append(ctdfJourney.Path, pathItem)
		}

		journeys = append(journeys, &ctdfJourney)
	}
	pretty.Println(journeys)

	log.Info().Msgf("Successfully imported into MongoDB")
}
