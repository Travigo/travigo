package routes

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/liip/sheriff"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func marshalSheriffMap(t *testing.T, groups []string, value interface{}) map[string]interface{} {
	t.Helper()
	reduced, err := sheriff.Marshal(&sheriff.Options{Groups: groups}, value)
	if err != nil {
		t.Fatalf("sheriff.Marshal() error = %v", err)
	}
	result, ok := reduced.(map[string]interface{})
	if !ok {
		t.Fatalf("sheriff.Marshal() returned %T, want map", reduced)
	}
	return result
}

func requireKeys(t *testing.T, value map[string]interface{}, expected ...string) {
	t.Helper()
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			t.Errorf("missing key %q in %#v", key, value)
		}
	}
}

func forbidKeys(t *testing.T, value map[string]interface{}, forbidden ...string) {
	t.Helper()
	for _, key := range forbidden {
		if _, ok := value[key]; ok {
			t.Errorf("unexpected key %q in %#v", key, value)
		}
	}
}

func TestWebStopMapSheriffView(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-1",
		OtherIdentifiers:  []string{"unused-alias"},
		PrimaryName:       "Central",
		Descriptor:        "Rail station",
		Timezone:          "Europe/London",
		Location:          &ctdf.Location{Type: "Point", Coordinates: []float64{-0.1, 51.5}},
		Services: []*ctdf.Service{{
			PrimaryIdentifier: "service-1", ServiceName: "A", OtherIdentifiers: []string{"unused-service-alias"},
			BrandColour: "112233", TransportType: ctdf.TransportTypeRail, OperatorRef: "unused-operator",
		}},
	}

	result := marshalSheriffMap(t, []string{"web-stop-map"}, stop)
	requireKeys(t, result, "PrimaryIdentifier", "PrimaryName", "Descriptor", "Timezone", "Location", "Services")
	forbidKeys(t, result, "OtherIdentifiers", "DataSource", "PlatformCode", "Active", "TransportTypes")

	services := result["Services"].([]interface{})
	service := services[0].(map[string]interface{})
	requireKeys(t, service, "PrimaryIdentifier", "ServiceName", "BrandColour", "TransportType")
	forbidKeys(t, service, "OtherIdentifiers", "OperatorRef", "Routes", "StopNameOverrides")
}

func TestWebOSMMapSheriffViewDoesNotExposeRawQueryOrElements(t *testing.T) {
	osmStop := &ctdf.OSMStop{
		PrimaryIdentifier: "osm-stop-1",
		Query:             ctdf.OSMStopQuery{OverpassQuery: "large internal query", Endpoint: "internal endpoint"},
		Elements:          []ctdf.OSMElement{{Type: ctdf.OSMElementTypeWay, ID: 99, Tags: map[string]string{"large": "raw"}}},
		Features: []ctdf.OSMStopFeature{{
			Type:        ctdf.OSMStopFeatureTypePlatform,
			Element:     ctdf.OSMElementRef{Type: ctdf.OSMElementTypeWay, ID: 99},
			PrimaryName: "Platform 1", LocalRef: "1", Tags: map[string]string{"name": "duplicate", "large": "unused"},
			Geometry: []ctdf.Location{{Type: "Point", Coordinates: []float64{-0.1, 51.5}}},
		}},
	}

	result := marshalSheriffMap(t, []string{"web-osm-map"}, osmStop)
	requireKeys(t, result, "Features")
	forbidKeys(t, result, "PrimaryIdentifier", "Query", "Elements", "Match", "Station", "StopArea")
	feature := result["Features"].([]interface{})[0].(map[string]interface{})
	requireKeys(t, feature, "Type", "Element", "PrimaryName", "LocalRef", "Geometry")
	forbidKeys(t, feature, "Tags", "Role", "ParkingConfidence", "DistanceMetres")
}

func TestWebRealtimeJourneySheriffViewKeepsOccurrenceIdentityWithoutNestedDomainGraph(t *testing.T) {
	realtime := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-1",
		Journey:           &ctdf.Journey{PrimaryIdentifier: "nested-journey", Path: []*ctdf.JourneyPathItem{{}}},
		DepartedStop:      &ctdf.Stop{PrimaryIdentifier: "departed"},
		NextStop:          &ctdf.Stop{PrimaryIdentifier: "next"},
		ActivelyTracked:   true,
		NextStopRef:       "stop-2",
		NextStopIndex:     3,
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-2@3": {StopRef: "stop-2", JourneyStopIndex: 3, Stop: &ctdf.Stop{PrimaryIdentifier: "unused-full-stop"}},
		},
		Reliability: ctdf.RealtimeJourneyReliabilityLocationWithTrack,
	}

	result := marshalSheriffMap(t, []string{"web-journey-realtime"}, realtime)
	requireKeys(t, result, "ActivelyTracked", "NextStopRef", "NextStopIndex", "Stops", "Reliability")
	forbidKeys(t, result, "PrimaryIdentifier", "Journey", "DepartedStop", "NextStop", "JourneyRunDate", "DataSource")
	stop := result["Stops"].(map[string]interface{})["stop-2@3"].(map[string]interface{})
	requireKeys(t, stop, "StopRef", "JourneyStopIndex")
	forbidKeys(t, stop, "Stop")
}

func TestWebBoardSheriffViewKeepsDisplayFieldsWithoutJourneyGraphs(t *testing.T) {
	board := &ctdf.DepartureBoard{
		Journey: &ctdf.Journey{
			PrimaryIdentifier: "journey-1",
			OtherIdentifiers:  map[string]string{"unused": "alias"},
			Service: &ctdf.Service{
				PrimaryIdentifier: "service-1",
				ServiceName:       "A",
				TransportType:     ctdf.TransportTypeRail,
				Routes:            []ctdf.Route{{Origin: "unused-heavy-route"}},
			},
			Path: []*ctdf.JourneyPathItem{{OriginStopRef: "unused-heavy-path"}},
			RealtimeJourney: &ctdf.RealtimeJourney{
				Reliability: ctdf.RealtimeJourneyReliabilityLocationWithoutTrack,
				Stops:       map[string]*ctdf.RealtimeJourneyStops{"unused": {StopRef: "unused"}},
			},
		},
		DestinationDisplay: "Destination",
		Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
		Platform:           "2",
		Time:               time.Now(),
	}

	result := marshalSheriffMap(t, []string{"web-board"}, board)
	requireKeys(t, result, "Journey", "DestinationDisplay", "Type", "Platform", "Time")
	journey := result["Journey"].(map[string]interface{})
	requireKeys(t, journey, "PrimaryIdentifier", "Service", "RealtimeJourney")
	forbidKeys(t, journey, "OtherIdentifiers", "Path", "Track", "DataSource", "Operator")
	service := journey["Service"].(map[string]interface{})
	requireKeys(t, service, "PrimaryIdentifier", "ServiceName", "TransportType")
	forbidKeys(t, service, "Routes", "OtherIdentifiers", "DataSource")
	realtime := journey["RealtimeJourney"].(map[string]interface{})
	requireKeys(t, realtime, "Reliability")
	forbidKeys(t, realtime, "Stops", "Journey", "VehicleLocation", "Occupancy")
}

func TestWebPlannerSheriffViewUsesSummaries(t *testing.T) {
	results := &ctdf.JourneyPlanResults{
		OriginStop: ctdf.Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin", OtherIdentifiers: []string{"unused"}},
		JourneyPlans: []ctdf.JourneyPlan{{
			RouteItems: []ctdf.JourneyPlanRouteItem{{
				Type: ctdf.JourneyPlanRouteItemTypeJourney,
				Journey: &ctdf.Journey{
					PrimaryIdentifier: "journey-1",
					Service:           &ctdf.Service{PrimaryIdentifier: "service-1", ServiceName: "A", TransportType: ctdf.TransportTypeRail},
					Path:              []*ctdf.JourneyPathItem{{OriginStopRef: "unused-heavy-path"}},
				},
				OriginStopRef: "origin",
				OriginStop:    &ctdf.Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"},
			}},
		}},
	}

	result := marshalSheriffMap(t, []string{"web-planner"}, results)
	plans := result["JourneyPlans"].([]interface{})
	item := plans[0].(map[string]interface{})["RouteItems"].([]interface{})[0].(map[string]interface{})
	requireKeys(t, item, "OriginStop", "Journey")
	journey := item["Journey"].(map[string]interface{})
	requireKeys(t, journey, "PrimaryIdentifier", "Service")
	forbidKeys(t, journey, "Path", "Track", "DetailedRailInformation", "DataSource")
}

func TestWebDatasourceSheriffViewExcludesAuthenticationConfiguration(t *testing.T) {
	source := datasets.DataSource{
		Identifier:           "provider",
		SourceAuthentication: &datasets.SourceAuthentication{AuthHeader: "SECRET_ENV_NAME"},
		Datasets: []datasets.DataSet{{
			Identifier: "schedule", Source: "https://example.com", CustomConfig: map[string]string{"unused": "value"},
		}},
	}
	result := marshalSheriffMap(t, []string{"web-datasource"}, source)
	requireKeys(t, result, "Identifier", "Datasets")
	forbidKeys(t, result, "SourceAuthentication")
	dataset := result["Datasets"].([]interface{})[0].(map[string]interface{})
	requireKeys(t, dataset, "Identifier", "Source", "SupportedObjects")
	forbidKeys(t, dataset, "CustomConfig", "RefreshInterval", "IgnoreObjects", "LinkedDataset")
}

func TestWebSavedSheriffViewEmbedsOnlyTheHydratedSummary(t *testing.T) {
	saved := &ctdf.SavedObject{
		PrimaryIdentifier: "saved-1",
		UserID:            "private-user-id",
		Type:              "Stop",
		ObjectIdentifier:  "stop-1",
		Object: &ctdf.Stop{
			PrimaryIdentifier: "stop-1",
			PrimaryName:       "Central",
			OtherIdentifiers:  []string{"unused-alias"},
			Services: []*ctdf.Service{{
				PrimaryIdentifier: "service-1",
				ServiceName:       "A",
				OtherIdentifiers:  []string{"unused-service-alias"},
			}},
		},
	}

	result := marshalSheriffMap(t, []string{"web-saved"}, saved)
	requireKeys(t, result, "PrimaryIdentifier", "Type", "ObjectIdentifier", "Object")
	forbidKeys(t, result, "UserID")
	stop := result["Object"].(map[string]interface{})
	requireKeys(t, stop, "PrimaryIdentifier", "PrimaryName", "Services")
	forbidKeys(t, stop, "OtherIdentifiers", "DataSource", "Location")
	service := stop["Services"].([]interface{})[0].(map[string]interface{})
	requireKeys(t, service, "PrimaryIdentifier", "ServiceName")
	forbidKeys(t, service, "OtherIdentifiers", "Routes", "StopNameOverrides")
}

func TestWebNotificationSubscriptionSheriffViewEmbedsResolvedReferences(t *testing.T) {
	subscription := &ctdf.UserNotificationSubscription{
		PrimaryIdentifier: "subscription-1",
		UserID:            "private-user-id",
		EventType:         ctdf.EventTypeRealtimeJourneyPlatformChanged,
		Values: ctdf.UserNotificationSubscriptionValues{
			JourneyRef: "journey-1",
			StopRefs:   []string{"stop-1"},
		},
		Subject: &ctdf.Journey{
			PrimaryIdentifier:  "journey-1",
			DestinationDisplay: "Destination",
			Path:               []*ctdf.JourneyPathItem{{OriginStopRef: "unused-heavy-path"}},
		},
		PlatformStops: []*ctdf.Stop{{
			PrimaryIdentifier: "stop-1",
			PrimaryName:       "Platform stop",
			OtherIdentifiers:  []string{"unused-alias"},
		}},
	}

	result := marshalSheriffMap(t, []string{"web-notification-subscription", "web-notification"}, subscription)
	requireKeys(t, result, "id", "eventType", "values", "subject", "platformStops")
	forbidKeys(t, result, "UserID", "Program")
	subject := result["subject"].(map[string]interface{})
	requireKeys(t, subject, "PrimaryIdentifier", "DestinationDisplay")
	forbidKeys(t, subject, "Path", "DataSource", "RealtimeJourney")
	platformStop := result["platformStops"].([]interface{})[0].(map[string]interface{})
	requireKeys(t, platformStop, "PrimaryIdentifier", "PrimaryName")
	forbidKeys(t, platformStop, "OtherIdentifiers", "Location", "Services")
}

func TestLegacyDatasourceJSONDoesNotExposeAuthenticationConfiguration(t *testing.T) {
	source := datasets.DataSource{
		Identifier:           "provider",
		SourceAuthentication: &datasets.SourceAuthentication{AuthHeader: "SECRET_ENV_NAME"},
	}
	payload, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	forbidKeys(t, result, "SourceAuthentication")
}

func TestWebViewsAreSmallerThanLegacyJourneyAndOSMPayloads(t *testing.T) {
	largeText := make([]byte, 32*1024)
	journey := &ctdf.Journey{
		PrimaryIdentifier: "journey-1",
		Service:           &ctdf.Service{PrimaryIdentifier: "service-1", ServiceName: "A"},
		RealtimeJourney: &ctdf.RealtimeJourney{
			Journey: &ctdf.Journey{PrimaryIdentifier: "duplicated-journey", Path: []*ctdf.JourneyPathItem{{OriginStop: &ctdf.Stop{PrimaryName: string(largeText)}}}},
		},
	}
	osmStop := &ctdf.OSMStop{
		Query:    ctdf.OSMStopQuery{OverpassQuery: string(largeText)},
		Elements: []ctdf.OSMElement{{Tags: map[string]string{"raw": string(largeText)}}},
		Features: []ctdf.OSMStopFeature{{Type: ctdf.OSMStopFeatureTypeStation}},
	}

	assertSmaller := func(name string, value interface{}, legacyGroups, webGroups []string) {
		t.Helper()
		legacy, _ := sheriff.Marshal(&sheriff.Options{Groups: legacyGroups}, value)
		web, _ := sheriff.Marshal(&sheriff.Options{Groups: webGroups}, value)
		legacyJSON, _ := json.Marshal(legacy)
		webJSON, _ := json.Marshal(web)
		if len(webJSON) >= len(legacyJSON) {
			t.Errorf("%s web payload = %d bytes, legacy = %d bytes", name, len(webJSON), len(legacyJSON))
		}
	}

	assertSmaller("journey", journey, []string{"basic", "detailed"}, []string{"web-journey"})
	assertSmaller("osm", osmStop, []string{"basic", "detailed", "internal"}, []string{"web-osm-map"})
}

func TestMarshalWithSheriffViewRejectsArbitraryGroups(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		_, err := marshalWithSheriffView(c, struct {
			Internal string `groups:"internal"`
		}{Internal: "secret"}, []string{"basic"}, sheriffViews{"web": {"web-stop-summary"}})
		if err != nil {
			return sheriffViewError(c, err)
		}
		return c.SendStatus(fiber.StatusOK)
	})

	response, err := app.Test(httptest.NewRequest("GET", "/?view=internal", nil), int(time.Second.Milliseconds()))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if response.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusBadRequest)
	}
}
