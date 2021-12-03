package transxchange

import (
	"errors"
	"fmt"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/kr/pretty"
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

func (doc *TransXChange) ImportIntoMongoAsCTDF() {
	// Map the local operator references to glboally unique operator codes based on NOC
	operatorLocalMapping := map[string]string{}

	for i := 0; i < len(doc.Operators); i++ {
		operator := doc.Operators[i]

		operatorLocalMapping[operator.ID] = fmt.Sprintf("UK:%s", operator.NationalOperatorCode)
	}

	// Get CTDF services from TransXChange Services & Lines
	for serviceID := 0; serviceID < len(doc.Services); serviceID++ {
		txcService := doc.Services[serviceID]

		for lineID := 0; lineID < len(txcService.Lines); lineID++ {
			txcLine := txcService.Lines[lineID]

			operatorRef := operatorLocalMapping[txcService.RegisteredOperatorRef]

			ctdfService := ctdf.Service{
				PrimaryIdentifier: fmt.Sprintf("UK:%s:%s:%s", operatorRef, txcService.ServiceCode, txcLine.ID),
				OtherIdentifiers: map[string]string{
					"ServiceCode": txcService.ServiceCode,
					"LineID":      txcLine.ID,
				},

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

			pretty.Println(ctdfService)
		}
	}
}
