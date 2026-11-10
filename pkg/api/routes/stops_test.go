package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

type boardTestSource struct {
	stop     *ctdf.Stop
	stopErr  error
	boardErr error
	queries  *[]query.DepartureBoard
	items    []*ctdf.DepartureBoard
}

func (s boardTestSource) GetName() string { return "board test" }
func (s boardTestSource) Supports() []reflect.Type {
	return []reflect.Type{reflect.TypeOf(ctdf.Stop{}), reflect.TypeOf([]*ctdf.DepartureBoard{})}
}
func (s boardTestSource) Lookup(q any) (interface{}, error) {
	switch v := q.(type) {
	case query.Stop:
		return s.stop, s.stopErr
	case query.DepartureBoard:
		*s.queries = append(*s.queries, v)
		return s.items, s.boardErr
	default:
		panic("unexpected query")
	}
}

func TestStopBoardCompactRetainsOccurrenceAndServiceDate(t *testing.T) {
	previous := dataaggregator.GlobalAggregator
	t.Cleanup(func() { dataaggregator.GlobalAggregator = previous })
	var queries []query.DepartureBoard
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
	dataaggregator.GlobalAggregator.RegisterSource(boardTestSource{
		stop: &ctdf.Stop{Timezone: "Europe/London"}, queries: &queries,
		items: []*ctdf.DepartureBoard{{OccurrenceIdentifier: "visit", ServiceDate: "2026-11-03", Time: time.Date(2026, 11, 4, 1, 30, 0, 0, time.UTC)}},
	})
	app := fiber.New()
	app.Get("/:identifier/departures", getStopDepartures)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/stop/departures?view=compact&datetime=2026-11-04T01:00:00Z", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var items []struct {
		OccurrenceIdentifier string
		ServiceDate          string
		Time                 time.Time
	}
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(items) != 1 || items[0].OccurrenceIdentifier != "visit" || items[0].ServiceDate != "2026-11-03" || items[0].Time.Hour() != 1 {
		t.Fatalf("unexpected compact board: status=%d items=%+v", response.StatusCode, items)
	}
}

func TestStopBoardReturnsEmptyArray(t *testing.T) {
	previous := dataaggregator.GlobalAggregator
	t.Cleanup(func() { dataaggregator.GlobalAggregator = previous })
	var queries []query.DepartureBoard
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
	dataaggregator.GlobalAggregator.RegisterSource(boardTestSource{stop: &ctdf.Stop{Timezone: "UTC"}, queries: &queries})
	app := fiber.New()
	app.Get("/:identifier/departures", getStopDepartures)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/stop/departures?view=compact", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var items []json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || items == nil || len(items) != 0 {
		t.Fatalf("expected empty array, got %#v (status %d)", items, response.StatusCode)
	}
}

func TestStopBoardValidationAndTimezone(t *testing.T) {
	previous := dataaggregator.GlobalAggregator
	t.Cleanup(func() { dataaggregator.GlobalAggregator = previous })
	var queries []query.DepartureBoard
	src := boardTestSource{stop: &ctdf.Stop{Timezone: "Europe/London"}, queries: &queries}
	setSource := func(s boardTestSource) {
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		dataaggregator.GlobalAggregator.RegisterSource(s)
	}
	app := fiber.New()
	app.Get("/:identifier/departures", getStopDepartures)
	app.Get("/:identifier/arrivals", getStopArrivals)
	request := func(path string, want int) {
		t.Helper()
		response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", path, response.StatusCode, want)
		}
	}
	setSource(src)
	for _, value := range []string{"", "-1", "0", "101", "abc"} {
		request("/stop/departures?count="+value, http.StatusBadRequest)
		request("/stop/arrivals?count="+value, http.StatusBadRequest)
	}
	request("/stop/departures?datetime=invalid", http.StatusBadRequest)
	request("/stop/departures?cursor=abc", http.StatusBadRequest)
	if len(queries) != 0 {
		t.Fatal("invalid requests reached board source")
	}
	request("/stop/departures?datetime=2026-07-01T23:30:00Z", http.StatusOK)
	request("/stop/arrivals?datetime=2026-07-02T00:30:00%2B01:00", http.StatusOK)
	if len(queries) != 2 || !queries[0].StartDateTime.Equal(queries[1].StartDateTime) || queries[0].StartDateTime.Day() != 2 || queries[0].StartDateTime.Location().String() != "Europe/London" {
		t.Fatalf("times not normalized: %#v", queries)
	}
	badZone := src
	badZone.stop = &ctdf.Stop{Timezone: "invalid/timezone"}
	setSource(badZone)
	request("/stop/departures", http.StatusInternalServerError)
	missing := src
	missing.stop = nil
	missing.stopErr = mongo.ErrNoDocuments
	setSource(missing)
	request("/stop/departures", http.StatusNotFound)
	unavailable := src
	unavailable.stopErr = errors.New("database unavailable")
	setSource(unavailable)
	request("/stop/departures", http.StatusBadGateway)
	failedBoard := src
	failedBoard.boardErr = errors.New("board unavailable")
	setSource(failedBoard)
	request("/stop/departures", http.StatusBadGateway)
}

func TestResolveStopDisplayNameUsesServiceOverride(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		OtherIdentifiers:  []string{"stop-other"},
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, map[string]string{
		"stop-other": "Overridden",
	})

	if displayName != "Overridden" {
		t.Fatalf("expected overridden display name, got %q", displayName)
	}
	if !overrideApplied {
		t.Fatal("expected override to be reported as applied")
	}
}

func TestResolveStopDisplayNameFallsBackToPrimaryName(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, nil)

	if displayName != "Original" {
		t.Fatalf("expected primary display name, got %q", displayName)
	}
	if overrideApplied {
		t.Fatal("did not expect override to be reported as applied")
	}
}

func TestStopSearchTransportTypesSupportsMultiSelect(t *testing.T) {
	app := fiber.New()
	var transportTypes []string
	app.Get("/", func(c *fiber.Ctx) error {
		transportTypes = stopSearchTransportTypes(c)
		return c.SendStatus(fiber.StatusNoContent)
	})

	query := url.Values{}
	query.Add("transporttype", "Bus,Rail")
	query.Add("transporttype", "Tram")
	query.Add("transport_type", " Rail,Metro ")
	request, err := app.Test(httptest.NewRequest("GET", "/?"+query.Encode(), nil))
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	request.Body.Close()

	expected := []string{"Bus", "Rail", "Tram", "Metro"}
	if !reflect.DeepEqual(transportTypes, expected) {
		t.Fatalf("transport types = %#v, want %#v", transportTypes, expected)
	}
}

type currentStopServicesTestSource struct {
	query *query.ServicesByStops
}

func (currentStopServicesTestSource) GetName() string { return "current stop services test" }

func (currentStopServicesTestSource) Supports() []reflect.Type {
	return []reflect.Type{reflect.TypeFor[map[string][]*ctdf.Service]()}
}

func (s currentStopServicesTestSource) Lookup(value any) (any, error) {
	q, ok := value.(query.ServicesByStops)
	if !ok {
		return nil, errors.New("unexpected lookup")
	}
	*s.query = q
	servicesByStop := make(map[string][]*ctdf.Service, len(q.Stops))
	for _, stop := range q.Stops {
		servicesByStop[stop.PrimaryIdentifier] = []*ctdf.Service{{PrimaryIdentifier: "service-1"}}
	}
	return servicesByStop, nil
}

func TestSearchServiceHydrationResolvesRelinkedPrimaryAsAlias(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("search hit aliases use current stop refs", func(mt *mtest.T) {
		previousDB, previousAggregator := database.Instance, dataaggregator.GlobalAggregator
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		var batchQuery query.ServicesByStops
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		dataaggregator.GlobalAggregator.RegisterSource(currentStopServicesTestSource{query: &batchQuery})
		mt.Cleanup(func() {
			database.Instance, dataaggregator.GlobalAggregator = previousDB, previousAggregator
		})

		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch, bson.D{
			{Key: "primaryidentifier", Value: "current-stop"},
			{Key: "otheridentifiers", Value: bson.A{"old-stop"}},
			{Key: "platforms", Value: bson.A{bson.D{{Key: "primaryidentifier", Value: "current-platform"}}}},
		}))
		hit := &ctdf.Stop{PrimaryIdentifier: "old-stop", PrimaryName: "Name from search index"}
		if err := attachSearchStopServices(context.Background(), []*ctdf.Stop{hit}); err != nil {
			mt.Fatal(err)
		}
		if len(batchQuery.Stops) != 1 {
			mt.Fatalf("hydrated stops = %#v, want one current stop", batchQuery.Stops)
		}
		current := batchQuery.Stops[0]
		if current.PrimaryIdentifier != "current-stop" || len(current.OtherIdentifiers) != 1 || current.OtherIdentifiers[0] != "old-stop" {
			mt.Fatalf("service lookup used stale stop refs: %#v", current)
		}
		if len(current.Platforms) != 1 || current.Platforms[0].PrimaryIdentifier != "current-platform" {
			mt.Fatalf("service lookup omitted current platforms: %#v", current.Platforms)
		}
		if hit.PrimaryName != "Name from search index" || len(hit.Services) != 1 || hit.Services[0].PrimaryIdentifier != "service-1" {
			mt.Fatalf("search hit was not hydrated without replacing its shape: %#v", hit)
		}
	})
}

func TestSearchServiceHydrationPrefersPrimaryOverAlias(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("primary identifier wins over another stop alias", func(mt *mtest.T) {
		previousDB, previousAggregator := database.Instance, dataaggregator.GlobalAggregator
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		var batchQuery query.ServicesByStops
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		dataaggregator.GlobalAggregator.RegisterSource(currentStopServicesTestSource{query: &batchQuery})
		mt.Cleanup(func() {
			database.Instance, dataaggregator.GlobalAggregator = previousDB, previousAggregator
		})

		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch,
			bson.D{{Key: "primaryidentifier", Value: "other-stop"}, {Key: "otheridentifiers", Value: bson.A{"shared-ref"}}},
			bson.D{{Key: "primaryidentifier", Value: "shared-ref"}},
		))
		hit := &ctdf.Stop{PrimaryIdentifier: "shared-ref"}
		if err := attachSearchStopServices(context.Background(), []*ctdf.Stop{hit}); err != nil {
			mt.Fatal(err)
		}
		if len(batchQuery.Stops) != 1 || batchQuery.Stops[0].PrimaryIdentifier != "shared-ref" {
			mt.Fatalf("resolved search stop = %#v, want primary stop shared-ref", batchQuery.Stops)
		}
	})
}

func TestUnbuiltStopServicesReturnUnavailable(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return stopServicesLookupError(c, stopservices.ErrUnbuilt)
	})
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}
