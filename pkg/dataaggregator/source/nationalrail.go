package source

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/util"
	"golang.org/x/exp/slices"
)

type NationalRailSource struct {
}

func (n NationalRailSource) GetName() string {
	return "GB National Rail"
}

func (n NationalRailSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (n NationalRailSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		query := q.(query.DepartureBoard)

		crs := query.Stop.OtherIdentifiers["Crs"]

		// If the stop has no CrsRef then give up
		if !slices.Contains(query.Stop.TransportTypes, ctdf.TransportTypeTrain) || crs == "" {
			return nil, UnsupportedSourceError
		}

		// Make the request to the gateway for the departures
		xmlFile, err := nationalRailGatewayLookup(fmt.Sprintf("departures/%s", crs))

		if err != nil {
			return nil, err
		}

		byteValue, _ := ioutil.ReadAll(xmlFile.Body)

		var nationalRailDepartures nationalRailwayDepBoardWithDetailsResponse
		xml.Unmarshal(byteValue, &nationalRailDepartures)

		var departureBoard []*ctdf.DepartureBoard
		now := time.Now()

		for _, departure := range nationalRailDepartures.DepartureBoardDetails.TrainServices {
			scheduledTimeOnly, _ := time.Parse("15:04", departure.Scheduled)

			scheduledTime := time.Date(
				now.Year(), now.Month(), now.Day(), scheduledTimeOnly.Hour(), scheduledTimeOnly.Minute(), 0, 0, now.Location(),
			)

			operatorRef := fmt.Sprintf(ctdf.OperatorTOCFormat, departure.OperatorCode)
			serviceRef := fmt.Sprintf("GB:RAILSERVICE:%s", departure.ServiceID)

			departureBoard = append(departureBoard, &ctdf.DepartureBoard{
				DestinationDisplay: departure.Destination.Name,
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime,

				Journey: &ctdf.Journey{
					PrimaryIdentifier: serviceRef,

					ServiceRef: serviceRef,
					Service: &ctdf.Service{
						PrimaryIdentifier: serviceRef,
						ServiceName:       "",
					},

					OperatorRef: operatorRef,
					Operator: &ctdf.Operator{
						PrimaryIdentifier: operatorRef,
						PrimaryName:       departure.Operator,
					},
				},
			})
		}

		return departureBoard, nil
	default:
		return nil, UnsupportedSourceError
	}
}

type nationalRailwayDepBoardWithDetailsResponse struct {
	// GetDepBoardWithDetailsResponse nationalRailwayDepBoardWithDetailsResponse `xml:"soap:Envelope>soap:Body>GetDepBoardWithDetailsResponse"`
	XMLName               xml.Name
	DepartureBoardDetails nationalRailwayGetStationBoardResult `xml:"Body>GetDepBoardWithDetailsResponse>GetStationBoardResult"`
}
type nationalRailwayGetStationBoardResult struct {
	XMLName           xml.Name
	GeneratedAt       string `xml:"generatedAt"`
	LocationName      string `xml:"locationName"`
	Crs               string `xml:"crs"`
	PlatformAvailable bool   `xml:"platformAvailable"`

	TrainServices []nationalRailwayService `xml:"trainServices>service"`
}
type nationalRailwayService struct {
	ServiceID string `xml:"serviceID"`
	RSID      string `xml:"rsid"`

	IsCancelled bool `xml:"isCancelled"`

	Operator     string `xml:"operator"`
	OperatorCode string `xml:"operatorCode"`

	CancelReason string `xml:"cancelReason"`
	DelayReason  string `xml:"delayReason"`

	Scheduled string `xml:"std"`
	Estimated string `xml:"etc"`

	Length int `xml:"length"`

	Origin      nationalRailwayLocation `xml:"origin>location"`
	Destination nationalRailwayLocation `xml:"destination>location"`
}

type nationalRailwayLocation struct {
	Name string `xml:"locationName"`
	Crs  string `xml:"crs"`
}

func nationalRailGatewayLookup(path string) (*http.Response, error) {
	endpoint := util.GetEnvironmentVariables()["BRITBUS_LDBWS_GATEWAY_ENDPOINT"]
	source := fmt.Sprintf("%s/%s", endpoint, path)

	req, _ := http.NewRequest("GET", source, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"}

	client := &http.Client{}
	resp, err := client.Do(req)

	return resp, err
}
