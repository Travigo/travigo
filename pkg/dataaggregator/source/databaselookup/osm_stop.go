package databaselookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	osmStopCollectionName    = "osm_stops"
	osmStopQueryVersion      = 3
	defaultOverpassTimeout   = 90 * time.Second
	defaultRailSearchRadius  = 700
	defaultBusSearchRadius   = 150
	defaultOtherSearchRadius = 300
)

type overpassResponse struct {
	Elements []overpassElement `json:"elements"`
	Remark   string            `json:"remark"`
}

type overpassEndpoint struct {
	URL      string
	Supports func(*ctdf.Location) bool
}

var publicOverpassEndpoints = []overpassEndpoint{
	{URL: "https://overpass-api.de/api/interpreter"},
	{URL: "https://maps.mail.ru/osm/tools/overpass/api/interpreter"},
	{
		URL:      "https://overpass.osm.ch/api/interpreter",
		Supports: supportsSwissLocation,
	},
}

type overpassElement struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`

	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`

	Tags     map[string]string `json:"tags"`
	Bounds   *overpassBounds   `json:"bounds"`
	Center   *overpassPoint    `json:"center"`
	Geometry []overpassPoint   `json:"geometry"`
	Nodes    []int64           `json:"nodes"`
	Members  []overpassMember  `json:"members"`
}

type overpassMember struct {
	Type     string          `json:"type"`
	Ref      int64           `json:"ref"`
	Role     string          `json:"role"`
	Geometry []overpassPoint `json:"geometry"`
}

type overpassPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type overpassBounds struct {
	MinLat float64 `json:"minlat"`
	MinLon float64 `json:"minlon"`
	MaxLat float64 `json:"maxlat"`
	MaxLon float64 `json:"maxlon"`
}

type osmStopQueryPlan struct {
	overpassQuery string
	method        ctdf.OSMStopMatchMethod
	matchedValue  string
	radiusMetres  int
	location      *ctdf.Location
}

type stopFeatureAssociationContext struct {
	roleByKey            map[string]string
	stationPolygons      [][]overpassPoint
	stationRetailAnchors []overpassPoint
}

func (s Source) OSMStopQuery(q query.OSMStop) (*ctdf.OSMStop, error) {
	if q.Stop == nil {
		return nil, errors.New("OSMStop query requires a Stop")
	}
	if q.Stop.PrimaryIdentifier == "" {
		return nil, errors.New("OSMStop query requires a Stop with a PrimaryIdentifier")
	}

	collection := database.GetCollection(osmStopCollectionName)
	if !q.ForceRefresh {
		cachedOSMStop, err := findCachedOSMStop(collection, q.Stop)
		if err == nil {
			return cachedOSMStop, nil
		}
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, err
		}
	}

	plan, err := buildOSMStopQueryPlan(q.Stop, q.RadiusMetres)
	if err != nil {
		return nil, err
	}

	overpassElements, endpoint, err := queryOverpass(plan.overpassQuery, plan.location)
	if err != nil {
		return nil, err
	}
	if len(overpassElements) == 0 {
		return nil, errors.New("overpass returned no OSM elements for stop")
	}

	selectedElements, stopArea, station := selectOSMStopElements(overpassElements, q.Stop)
	if len(selectedElements) == 0 {
		selectedElements = overpassElements
	}

	now := time.Now()
	osmStop := &ctdf.OSMStop{
		PrimaryIdentifier:    fmt.Sprintf(ctdf.OSMStopIDFormat, q.Stop.PrimaryIdentifier),
		OtherIdentifiers:     buildOSMStopOtherIdentifiers(stopArea, station),
		CreationDateTime:     now,
		ModificationDateTime: now,
		StopRef:              q.Stop.PrimaryIdentifier,
		TransportTypes:       q.Stop.TransportTypes,
		Match: ctdf.OSMStopMatch{
			Method:         plan.method,
			MatchedValue:   plan.matchedValue,
			Confidence:     osmStopMatchConfidence(plan.method, stopArea),
			DistanceMetres: osmStopDistanceToSelection(q.Stop, stopArea, station),
		},
		Query: ctdf.OSMStopQuery{
			Version:       osmStopQueryVersion,
			OverpassQuery: plan.overpassQuery,
			Endpoint:      endpoint,
			QueriedAt:     now,
			Location:      plan.location,
			RadiusMetres:  plan.radiusMetres,
		},
		Features: buildOSMStopFeatures(selectedElements, stopArea),
		Elements: buildOSMElements(selectedElements),
	}

	if stopArea != nil {
		ref := osmElementRef(*stopArea)
		osmStop.StopArea = &ref
	}
	if station != nil {
		ref := osmElementRef(*station)
		osmStop.Station = &ref
	}

	opts := options.Update().SetUpsert(true)
	_, err = collection.UpdateOne(
		context.Background(),
		bson.M{"primaryidentifier": osmStop.PrimaryIdentifier},
		bson.M{"$set": osmStop},
		opts,
	)
	if err != nil {
		return nil, err
	}

	return osmStop, nil
}

func findCachedOSMStop(collection *mongo.Collection, stop *ctdf.Stop) (*ctdf.OSMStop, error) {
	var osmStop ctdf.OSMStop
	err := collection.FindOne(context.Background(), bson.M{"stopref": stop.PrimaryIdentifier}).Decode(&osmStop)
	if err != nil {
		return nil, err
	}
	if !osmStopCacheIsCurrent(&osmStop) {
		return nil, mongo.ErrNoDocuments
	}

	return &osmStop, nil
}

func osmStopCacheIsCurrent(osmStop *ctdf.OSMStop) bool {
	return osmStop != nil && osmStop.Query.Version == osmStopQueryVersion
}

func buildOSMStopQueryPlan(stop *ctdf.Stop, radiusMetres int) (osmStopQueryPlan, error) {
	crsValues := identifierValues(stop, "gb-crs-")
	tiplocValues := identifierValues(stop, "gb-tiploc-")
	atcoValues := identifierValues(stop, "gb-atco-")
	gtfsStopIDValues := identifierValues(stop, "gtfs-stop-")
	crs := firstValue(crsValues)
	tiploc := firstValue(tiplocValues)
	atco := firstValue(atcoValues)
	gtfsStopID := firstValue(gtfsStopIDValues)

	if crs != "" || tiploc != "" || atco != "" || gtfsStopID != "" {
		queryParts := make([]string, 0, 3*(len(crsValues)+len(tiplocValues)+len(atcoValues)+len(gtfsStopIDValues)))
		for _, value := range crsValues {
			queryParts = append(queryParts, osmNWRTagLookup("ref:crs", value)...)
		}
		for _, value := range tiplocValues {
			queryParts = append(queryParts, osmNWRTagLookup("ref:tiploc", value)...)
		}
		for _, value := range atcoValues {
			queryParts = append(queryParts, osmNWRTagLookup("naptan:AtcoCode", value)...)
		}
		for _, value := range gtfsStopIDValues {
			queryParts = append(queryParts, osmNWRTagLookup("gtfs:stop_id", value)...)
		}

		method, matchedValue := bestIdentifierMatch(crs, tiploc, atco, gtfsStopID)
		return osmStopQueryPlan{
			overpassQuery: buildOSMStopExactOverpassQuery(strings.Join(queryParts, "\n")),
			method:        method,
			matchedValue:  matchedValue,
			radiusMetres:  0,
			location:      stop.Location,
		}, nil
	}

	if stop.Location == nil || len(stop.Location.Coordinates) < 2 {
		return osmStopQueryPlan{}, errors.New("OSMStop query requires CRS/TIPLOC/NaPTAN/GTFS identifier or stop location")
	}

	radius := radiusMetres
	if radius == 0 {
		radius = defaultRadius(stop)
	}

	return osmStopQueryPlan{
		overpassQuery: buildOSMStopCoordinateOverpassQuery(stop.Location, radius),
		method:        ctdf.OSMStopMatchMethodCoordinate,
		matchedValue:  fmt.Sprintf("%f,%f", stop.Location.Coordinates[1], stop.Location.Coordinates[0]),
		radiusMetres:  radius,
		location:      stop.Location,
	}, nil
}

func buildOSMStopExactOverpassQuery(stationLookup string) string {
	return fmt.Sprintf(`[out:json][timeout:60];
(
%s
)->.station;

(
  relation(bn.station);
  relation(bw.station);
  relation(br.station);
)->.station_parents;

relation.station_parents
  ["type"="public_transport"]
  ["public_transport"="stop_area"]
->.stop_area;

node(r.stop_area)->.member_nodes;
way(r.stop_area)->.member_ways;
relation(r.stop_area)->.member_relations;
node(w.member_ways)->.member_way_nodes;

node(r.member_relations)->.nested_nodes;
way(r.member_relations)->.nested_ways;
relation(r.member_relations)->.nested_relations;
node(w.nested_ways)->.nested_way_nodes;

node.member_nodes["public_transport"="stop_position"]->.stop_positions;
node.station["public_transport"="stop_position"]->.matched_stop_positions;
way.station["railway"="platform"]->.matched_platform_ways;
(
  .stop_positions;
  .matched_stop_positions;
)->.all_stop_positions;

(
  way(bn.all_stop_positions)["railway"~"^(rail|light_rail|subway|tram)$"];
  way(around.all_stop_positions:15)["railway"~"^(rail|light_rail|subway|tram)$"];
)
->.tracks_at_stops;

way(bn.all_stop_positions)
  ["highway"]
->.roads_at_stops;

way.member_ways["railway"="platform"]->.platform_ways;
way(around.all_stop_positions:40)["railway"="platform"]->.platform_ways_near_stops;
(
  .platform_ways;
  .platform_ways_near_stops;
  .matched_platform_ways;
)->.all_platform_ways;
node(w.all_platform_ways)->.platform_nodes;

way(around.platform_nodes:15)
  ["railway"~"^(rail|light_rail|subway|tram)$"]
->.tracks_near_platforms;

way(bn.platform_nodes)
  ["railway"="platform_edge"]
->.platform_edges_from_platforms;

(
  .station;
  .stop_area;
  .member_ways;
  .nested_ways;
)->.station_area_sources;
.station_area_sources map_to_area -> .station_areas;

(
  node(area.station_areas)["shop"];
  way(area.station_areas)["shop"];
  relation(area.station_areas)["shop"];
  node(around.member_nodes:25)["shop"];
  way(around.member_nodes:25)["shop"];
  relation(around.member_nodes:25)["shop"];
  node(around.member_way_nodes:25)["shop"];
  way(around.member_way_nodes:25)["shop"];
  relation(around.member_way_nodes:25)["shop"];
  node(around.nested_nodes:25)["shop"];
  way(around.nested_nodes:25)["shop"];
  relation(around.nested_nodes:25)["shop"];
  node(around.nested_way_nodes:25)["shop"];
  way(around.nested_way_nodes:25)["shop"];
  relation(around.nested_way_nodes:25)["shop"];
  node(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
)->.candidate_pois;

(
  node(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
)->.candidate_parking;

(
  .station;
  relation.stop_area;
  .member_nodes;
  .member_ways;
  .member_relations;
  .nested_nodes;
  .nested_ways;
  .nested_relations;
  .tracks_at_stops;
  .tracks_near_platforms;
  .roads_at_stops;
  .all_platform_ways;
  .platform_edges_from_platforms;
  .candidate_pois;
  .candidate_parking;
);
out body geom;`, stationLookup)
}

func buildOSMStopCoordinateOverpassQuery(location *ctdf.Location, radiusMetres int) string {
	lat := location.Coordinates[1]
	lon := location.Coordinates[0]

	return fmt.Sprintf(`[out:json][timeout:60];
relation(around:%d,%f,%f)
  ["type"="public_transport"]
  ["public_transport"="stop_area"]
->.stop_area;

node(r.stop_area)->.member_nodes;
way(r.stop_area)->.member_ways;
relation(r.stop_area)->.member_relations;
node(w.member_ways)->.member_way_nodes;

node(r.member_relations)->.nested_nodes;
way(r.member_relations)->.nested_ways;
relation(r.member_relations)->.nested_relations;
node(w.nested_ways)->.nested_way_nodes;

node.member_nodes["public_transport"="stop_position"]->.stop_positions;
(
  .stop_positions;
)->.all_stop_positions;

(
  way(bn.all_stop_positions)["railway"~"^(rail|light_rail|subway|tram)$"];
  way(around.all_stop_positions:15)["railway"~"^(rail|light_rail|subway|tram)$"];
)
->.tracks_at_stops;

way(bn.all_stop_positions)
  ["highway"]
->.roads_at_stops;

way.member_ways["railway"="platform"]->.platform_ways;
way(around.all_stop_positions:40)["railway"="platform"]->.platform_ways_near_stops;
(
  .platform_ways;
  .platform_ways_near_stops;
)->.all_platform_ways;
node(w.all_platform_ways)->.platform_nodes;

way(around.platform_nodes:15)
  ["railway"~"^(rail|light_rail|subway|tram)$"]
->.tracks_near_platforms;

way(bn.platform_nodes)
  ["railway"="platform_edge"]
->.platform_edges_from_platforms;

(
  .stop_area;
  .member_ways;
  .nested_ways;
)->.station_area_sources;
.station_area_sources map_to_area -> .station_areas;

(
  node(area.station_areas)["shop"];
  way(area.station_areas)["shop"];
  relation(area.station_areas)["shop"];
  node(around.member_nodes:25)["shop"];
  way(around.member_nodes:25)["shop"];
  relation(around.member_nodes:25)["shop"];
  node(around.member_way_nodes:25)["shop"];
  way(around.member_way_nodes:25)["shop"];
  relation(around.member_way_nodes:25)["shop"];
  node(around.nested_nodes:25)["shop"];
  way(around.nested_nodes:25)["shop"];
  relation(around.nested_nodes:25)["shop"];
  node(around.nested_way_nodes:25)["shop"];
  way(around.nested_way_nodes:25)["shop"];
  relation(around.nested_way_nodes:25)["shop"];
  node(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(area.station_areas)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.member_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.member_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.nested_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  node(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  way(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
  relation(around.nested_way_nodes:20)["amenity"~"^(cafe|restaurant|fast_food|food_court|pub|bar|toilets|atm|bank|pharmacy)$"];
)->.candidate_pois;

(
  node(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.member_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.member_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.nested_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  node(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  way(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
  relation(around.nested_way_nodes:300)["amenity"~"^(parking|bicycle_parking|motorcycle_parking)$"];
)->.candidate_parking;

(
  relation.stop_area;
  .member_nodes;
  .member_ways;
  .member_relations;
  .nested_nodes;
  .nested_ways;
  .nested_relations;
  .tracks_at_stops;
  .tracks_near_platforms;
  .roads_at_stops;
  .all_platform_ways;
  .platform_edges_from_platforms;
  .candidate_pois;
  .candidate_parking;
);
out body geom;`, radiusMetres, lat, lon)
}

func queryOverpass(overpassQuery string, location *ctdf.Location) ([]overpassElement, string, error) {
	return queryOverpassWithEndpoints(overpassQuery, overpassEndpointsForLocation(location))
}

func queryOverpassWithEndpoints(overpassQuery string, endpoints []string) ([]overpassElement, string, error) {
	form := url.Values{}
	form.Set("data", overpassQuery)

	errorsByEndpoint := make([]error, 0, len(endpoints))
	for _, endpoint := range endpoints {
		elements, err := querySingleOverpassEndpoint(endpoint, form)
		if err == nil && len(elements) > 0 {
			return elements, endpoint, nil
		}
		if err == nil {
			err = errors.New("returned no OSM elements")
		}
		errorsByEndpoint = append(errorsByEndpoint, fmt.Errorf("%s: %w", endpoint, err))
	}

	return nil, "", fmt.Errorf("all Overpass endpoints failed: %w", errors.Join(errorsByEndpoint...))
}

func querySingleOverpassEndpoint(endpoint string, form url.Values) ([]overpassElement, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultOverpassTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("user-agent", "travigo/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("returned status %d: %s", resp.StatusCode, string(body))
	}

	var parsed overpassResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if parsed.Remark != "" {
		return nil, errors.New(parsed.Remark)
	}

	return parsed.Elements, nil
}

func overpassEndpointsForLocation(location *ctdf.Location) []string {
	endpoints := make([]string, 0, len(publicOverpassEndpoints)+1)
	seen := map[string]struct{}{}
	addEndpoint := func(endpoint string) {
		if endpoint == "" {
			return
		}
		if _, exists := seen[endpoint]; exists {
			return
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}

	// A configured endpoint remains preferred, but no longer turns an outage into
	// a failed lookup when a public endpoint is available.
	addEndpoint(util.GetEnvironmentVariables()["TRAVIGO_OVERPASS_ENDPOINT"])

	publicEndpoints := make([]string, 0, len(publicOverpassEndpoints))
	for _, endpoint := range publicOverpassEndpoints {
		if endpoint.Supports == nil || endpoint.Supports(location) {
			publicEndpoints = append(publicEndpoints, endpoint.URL)
		}
	}
	rand.Shuffle(len(publicEndpoints), func(i int, j int) {
		publicEndpoints[i], publicEndpoints[j] = publicEndpoints[j], publicEndpoints[i]
	})
	for _, endpoint := range publicEndpoints {
		addEndpoint(endpoint)
	}

	return endpoints
}

func supportsSwissLocation(location *ctdf.Location) bool {
	if location == nil || len(location.Coordinates) < 2 {
		return false
	}
	lon := location.Coordinates[0]
	lat := location.Coordinates[1]
	return lat >= 45.8 && lat <= 47.9 && lon >= 5.8 && lon <= 10.6
}

func osmNWRTagLookup(key string, value string) []string {
	quotedKey := overpassQuote(key)
	quotedValue := overpassQuote(value)

	return []string{
		fmt.Sprintf(`  node[%s=%s];`, quotedKey, quotedValue),
		fmt.Sprintf(`  way[%s=%s];`, quotedKey, quotedValue),
		fmt.Sprintf(`  relation[%s=%s];`, quotedKey, quotedValue),
	}
}

func overpassQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func selectOSMStopElements(elements []overpassElement, stop *ctdf.Stop) ([]overpassElement, *overpassElement, *overpassElement) {
	stopArea := selectBestStopArea(elements, stop)
	station := selectBestStation(elements, stopArea)

	if stopArea == nil {
		return elements, nil, station
	}

	included := map[string]bool{
		overpassElementKey(*stopArea): true,
	}

	stopPositionNodeIDs := map[int64]bool{}
	platformNodeIDs := map[int64]bool{}
	stationPolygons := [][]overpassPoint{}
	stationRetailAnchors := []overpassPoint{}

	byKey := mapOverpassElementsByKey(elements)
	for _, member := range stopArea.Members {
		key := overpassElementRefKey(member.Type, member.Ref)
		included[key] = true
		stationPolygons = append(stationPolygons, stationContainmentMemberPolygons(member)...)

		memberElement, exists := byKey[key]
		if !exists {
			continue
		}

		if isStopPosition(memberElement) && memberElement.Type == string(ctdf.OSMElementTypeNode) {
			stopPositionNodeIDs[memberElement.ID] = true
		}
		if isPlatform(memberElement) {
			for _, nodeID := range memberElement.Nodes {
				platformNodeIDs[nodeID] = true
			}
		}
		if isStationRetailAnchor(memberElement) {
			stationRetailAnchors = append(stationRetailAnchors, anchorPoints(memberElement)...)
		}
		stationPolygons = append(stationPolygons, stationContainmentPolygons(memberElement, member.Role)...)

		for _, nestedMember := range memberElement.Members {
			nestedKey := overpassElementRefKey(nestedMember.Type, nestedMember.Ref)
			included[nestedKey] = true
			stationPolygons = append(stationPolygons, stationContainmentMemberPolygons(nestedMember)...)
			if nestedElement, exists := byKey[nestedKey]; exists {
				stationPolygons = append(stationPolygons, stationContainmentPolygons(nestedElement, nestedMember.Role)...)
			}
			if nestedElement, exists := byKey[nestedKey]; exists && isStationRetailAnchor(nestedElement) {
				stationRetailAnchors = append(stationRetailAnchors, anchorPoints(nestedElement)...)
			}
		}
	}

	// NaPTAN and GTFS identifiers are often attached directly to platform ways
	// that are missing from an otherwise valid stop_area relation.
	stopIdentifiersByTag := buildStopIdentifiersByTag(stop)
	for _, element := range elements {
		if !elementMatchesStopIdentifiers(element, stopIdentifiersByTag) {
			continue
		}
		included[overpassElementKey(element)] = true
		if isStopPosition(element) && element.Type == string(ctdf.OSMElementTypeNode) {
			stopPositionNodeIDs[element.ID] = true
		}
		if isPlatform(element) {
			for _, nodeID := range element.Nodes {
				platformNodeIDs[nodeID] = true
			}
		}
	}

	for _, element := range elements {
		if included[overpassElementKey(element)] {
			continue
		}

		if isTrack(element) {
			included[overpassElementKey(element)] = true
		}

		if isRoad(element) {
			for _, nodeID := range element.Nodes {
				if stopPositionNodeIDs[nodeID] {
					included[overpassElementKey(element)] = true
					break
				}
			}
		}

		if isPlatformEdge(element) {
			for _, nodeID := range element.Nodes {
				if platformNodeIDs[nodeID] {
					included[overpassElementKey(element)] = true
					break
				}
			}
		}

		if isStationParking(element) ||
			(isStationRetailPOI(element) &&
				(elementInsideAnyPolygon(element, stationPolygons) ||
					hasStationContext(element) ||
					elementNearAnyPoint(element, stationRetailAnchors, 60))) {
			included[overpassElementKey(element)] = true
		}
	}

	selected := make([]overpassElement, 0, len(included))
	for _, element := range elements {
		if included[overpassElementKey(element)] {
			selected = append(selected, element)
		}
	}

	return selected, stopArea, station
}

func buildStopIdentifiersByTag(stop *ctdf.Stop) map[string]map[string]struct{} {
	prefixesByTag := map[string]string{
		"ref:crs":         "gb-crs-",
		"ref:tiploc":      "gb-tiploc-",
		"naptan:AtcoCode": "gb-atco-",
		"gtfs:stop_id":    "gtfs-stop-",
	}
	identifiersByTag := make(map[string]map[string]struct{}, len(prefixesByTag))
	for tag, prefix := range prefixesByTag {
		identifiers := identifierValues(stop, prefix)
		if len(identifiers) == 0 {
			continue
		}
		values := make(map[string]struct{}, len(identifiers))
		for _, identifier := range identifiers {
			values[identifier] = struct{}{}
		}
		identifiersByTag[tag] = values
	}
	return identifiersByTag
}

func elementMatchesStopIdentifiers(element overpassElement, identifiersByTag map[string]map[string]struct{}) bool {
	for tag, identifiers := range identifiersByTag {
		if _, matches := identifiers[element.Tags[tag]]; matches {
			return true
		}
	}
	return false
}

func selectBestStopArea(elements []overpassElement, stop *ctdf.Stop) *overpassElement {
	var best *overpassElement
	bestScore := math.MaxFloat64

	for i := range elements {
		element := &elements[i]
		if !isStopArea(*element) {
			continue
		}

		score := 0.0
		if stop.Location != nil {
			if point, ok := representativePoint(*element); ok {
				score = haversineMetres(stop.Location.Coordinates[1], stop.Location.Coordinates[0], point.Lat, point.Lon)
			}
		}

		if !stopAreaMatchesMode(*element, elements, stop.TransportTypes) {
			score += 10000
		}

		if score < bestScore {
			best = element
			bestScore = score
		}
	}

	return best
}

func selectBestStation(elements []overpassElement, stopArea *overpassElement) *overpassElement {
	if stopArea != nil {
		byKey := mapOverpassElementsByKey(elements)
		for _, member := range stopArea.Members {
			element, exists := byKey[overpassElementRefKey(member.Type, member.Ref)]
			if exists && isStation(element) {
				return &element
			}
		}
	}

	for i := range elements {
		if isStation(elements[i]) {
			return &elements[i]
		}
	}

	return nil
}

func stopAreaMatchesMode(stopArea overpassElement, elements []overpassElement, transportTypes []ctdf.TransportType) bool {
	if len(transportTypes) == 0 {
		return true
	}

	byKey := mapOverpassElementsByKey(elements)
	for _, member := range stopArea.Members {
		if memberElement, exists := byKey[overpassElementRefKey(member.Type, member.Ref)]; exists {
			for _, transportType := range transportTypes {
				if elementMatchesTransportType(memberElement, transportType) {
					return true
				}
			}
		}
	}

	return false
}

func elementMatchesTransportType(element overpassElement, transportType ctdf.TransportType) bool {
	switch transportType {
	case ctdf.TransportTypeRail:
		return element.Tags["train"] == "yes" || element.Tags["railway"] == "station" || element.Tags["railway"] == "halt" || isTrack(element)
	case ctdf.TransportTypeMetro:
		return element.Tags["subway"] == "yes" || element.Tags["railway"] == "subway" || element.Tags["station"] == "subway"
	case ctdf.TransportTypeTram:
		return element.Tags["tram"] == "yes" || element.Tags["railway"] == "tram"
	case ctdf.TransportTypeBus, ctdf.TransportTypeCoach:
		return element.Tags["bus"] == "yes" || element.Tags["highway"] == "bus_stop" || element.Tags["amenity"] == "bus_station"
	case ctdf.TransportTypeFerry:
		return element.Tags["ferry"] == "yes" || element.Tags["amenity"] == "ferry_terminal"
	default:
		return true
	}
}

func buildOSMStopFeatures(elements []overpassElement, stopArea *overpassElement) []ctdf.OSMStopFeature {
	associationContext := buildStopFeatureAssociationContext(elements, stopArea)

	features := make([]ctdf.OSMStopFeature, 0, len(elements))
	for _, element := range elements {
		featureType := classifyOSMStopFeature(element)
		if featureType == ctdf.OSMStopFeatureTypeOther {
			continue
		}

		role := associationContext.roleByKey[overpassElementKey(element)]
		parkingAssociation, parkingConfidence := classifyParkingAssociation(element, role)
		if isStationParking(element) && parkingAssociation == ctdf.OSMStopParkingUnrelated {
			continue
		}

		association, distanceMetres := classifyStopFeatureAssociation(element, role, parkingAssociation, associationContext)
		feature := ctdf.OSMStopFeature{
			Type:               featureType,
			Element:            osmElementRef(element),
			Role:               role,
			PrimaryName:        element.Tags["name"],
			Ref:                element.Tags["ref"],
			LocalRef:           element.Tags["local_ref"],
			Tags:               element.Tags,
			ParkingAssociation: parkingAssociation,
			ParkingConfidence:  parkingConfidence,
			Association:        association,
			DistanceMetres:     distanceMetres,
			Geometry:           overpassGeometryToLocations(element.Geometry),
		}

		if point, ok := representativePoint(element); ok {
			feature.Location = overpassPointToLocation(point)
		}

		features = append(features, feature)
	}

	return features
}

func buildStopFeatureAssociationContext(elements []overpassElement, stopArea *overpassElement) stopFeatureAssociationContext {
	context := stopFeatureAssociationContext{
		roleByKey: map[string]string{},
	}
	if stopArea == nil {
		return context
	}

	byKey := mapOverpassElementsByKey(elements)
	for _, member := range stopArea.Members {
		key := overpassElementRefKey(member.Type, member.Ref)
		context.roleByKey[key] = member.Role
		context.stationPolygons = append(context.stationPolygons, stationContainmentMemberPolygons(member)...)

		memberElement, exists := byKey[key]
		if !exists {
			continue
		}

		if isStationRetailAnchor(memberElement) {
			context.stationRetailAnchors = append(context.stationRetailAnchors, anchorPoints(memberElement)...)
		}
		context.stationPolygons = append(context.stationPolygons, stationContainmentPolygons(memberElement, member.Role)...)

		for _, nestedMember := range memberElement.Members {
			nestedKey := overpassElementRefKey(nestedMember.Type, nestedMember.Ref)
			context.roleByKey[nestedKey] = nestedMember.Role
			context.stationPolygons = append(context.stationPolygons, stationContainmentMemberPolygons(nestedMember)...)

			nestedElement, exists := byKey[nestedKey]
			if !exists {
				continue
			}
			if isStationRetailAnchor(nestedElement) {
				context.stationRetailAnchors = append(context.stationRetailAnchors, anchorPoints(nestedElement)...)
			}
			context.stationPolygons = append(context.stationPolygons, stationContainmentPolygons(nestedElement, nestedMember.Role)...)
		}
	}

	return context
}

func classifyStopFeatureAssociation(
	element overpassElement,
	role string,
	parkingAssociation ctdf.OSMStopParkingAssociation,
	context stopFeatureAssociationContext,
) (ctdf.OSMStopFeatureAssociation, float64) {
	if isStopArea(element) ||
		isStation(element) ||
		isStopPosition(element) ||
		isPlatform(element) ||
		isPlatformEdge(element) ||
		isEntrance(element) ||
		isTrack(element) ||
		isRoad(element) ||
		isAccess(element) ||
		role != "" ||
		elementInsideAnyPolygon(element, context.stationPolygons) ||
		hasStationContext(element) {
		return ctdf.OSMStopFeatureAssociationInside, 0
	}

	if elementNearAnyPoint(element, context.stationRetailAnchors, 60) ||
		isStationRetailPOI(element) ||
		parkingAssociation == ctdf.OSMStopParkingNearby ||
		parkingAssociation == ctdf.OSMStopParkingOfficial ||
		parkingAssociation == ctdf.OSMStopParkingLikely {
		distanceMetres, _ := nearestElementDistanceToPoints(element, context.stationRetailAnchors)
		return ctdf.OSMStopFeatureAssociationNearby, distanceMetres
	}

	return "", 0
}

func buildOSMElements(elements []overpassElement) []ctdf.OSMElement {
	out := make([]ctdf.OSMElement, 0, len(elements))
	for _, element := range elements {
		osmElement := ctdf.OSMElement{
			Type:     ctdf.OSMElementType(element.Type),
			ID:       element.ID,
			Tags:     element.Tags,
			Geometry: overpassGeometryToLocations(element.Geometry),
			Members:  buildOSMRelationMembers(element.Members),
		}

		if element.Bounds != nil {
			osmElement.Bounds = &ctdf.OSMBounds{
				MinLat: element.Bounds.MinLat,
				MinLon: element.Bounds.MinLon,
				MaxLat: element.Bounds.MaxLat,
				MaxLon: element.Bounds.MaxLon,
			}
		}
		if element.Center != nil {
			osmElement.Center = overpassPointToLocation(*element.Center)
		}
		if element.Type == string(ctdf.OSMElementTypeNode) {
			osmElement.Point = overpassPointToLocation(overpassPoint{Lat: element.Lat, Lon: element.Lon})
		}

		out = append(out, osmElement)
	}

	return out
}

func buildOSMRelationMembers(members []overpassMember) []ctdf.OSMRelationMember {
	out := make([]ctdf.OSMRelationMember, 0, len(members))
	for _, member := range members {
		out = append(out, ctdf.OSMRelationMember{
			Type:     ctdf.OSMElementType(member.Type),
			ID:       member.Ref,
			Role:     member.Role,
			Geometry: overpassGeometryToLocations(member.Geometry),
		})
	}

	return out
}

func buildOSMStopOtherIdentifiers(stopArea *overpassElement, station *overpassElement) []string {
	ids := []string{}
	if stopArea != nil {
		ids = append(ids, fmt.Sprintf("osm-relation-%d", stopArea.ID))
	}
	if station != nil {
		ids = append(ids, fmt.Sprintf("osm-%s-%d", station.Type, station.ID))
	}
	return ids
}

func classifyParkingAssociation(element overpassElement, role string) (ctdf.OSMStopParkingAssociation, float64) {
	if !isStationParking(element) {
		return "", 0
	}

	score := 0

	if role != "" {
		score += 4
	}
	if parkingHasStationContext(element) {
		score += 4
	}
	if parkingHasRailOperatorContext(element) {
		score += 3
	}
	if parkingHasNearbyContext(element) {
		score += 1
	}
	if parkingHasUnrelatedContext(element) {
		score -= 4
	}
	if parkingHasRestrictedAccess(element) {
		score -= 2
	}

	switch {
	case score >= 7:
		return ctdf.OSMStopParkingOfficial, 0.95
	case score >= 4:
		return ctdf.OSMStopParkingLikely, 0.8
	case score >= 0:
		return ctdf.OSMStopParkingNearby, 0.55
	default:
		return ctdf.OSMStopParkingUnrelated, 0.2
	}
}

func parkingHasStationContext(element overpassElement) bool {
	for _, value := range parkingContextValues(element) {
		if strings.Contains(value, "station") ||
			strings.Contains(value, "rail") ||
			strings.Contains(value, "train") ||
			strings.Contains(value, "cyclepoint") {
			return true
		}
	}

	return false
}

func parkingHasRailOperatorContext(element overpassElement) bool {
	for _, value := range parkingContextValues(element) {
		if strings.Contains(value, "network rail") ||
			strings.Contains(value, "national rail") ||
			strings.Contains(value, "greater anglia") ||
			strings.Contains(value, "gtr") ||
			strings.Contains(value, "govia thameslink") ||
			strings.Contains(value, "national car parks") ||
			strings.Contains(value, " ncp") ||
			strings.Contains(value, "apcoa") ||
			strings.Contains(value, "saba") {
			return true
		}
	}

	return false
}

func parkingHasNearbyContext(element overpassElement) bool {
	access := strings.ToLower(element.Tags["access"])
	return access == "" || access == "yes" || access == "destination" || access == "customers"
}

func parkingHasUnrelatedContext(element overpassElement) bool {
	for _, value := range parkingContextValues(element) {
		if strings.Contains(value, "travelodge") ||
			strings.Contains(value, "leisure park") ||
			strings.Contains(value, "cambridge city council") ||
			strings.Contains(value, "gwydir street") ||
			strings.Contains(value, "residential") {
			return true
		}
	}

	return false
}

func parkingHasRestrictedAccess(element overpassElement) bool {
	access := strings.ToLower(element.Tags["access"])
	return access == "private" || access == "no" || access == "permit"
}

func parkingContextValues(element overpassElement) []string {
	values := []string{
		element.Tags["name"],
		element.Tags["operator"],
		element.Tags["description"],
		element.Tags["note"],
		element.Tags["location"],
		element.Tags["website"],
		element.Tags["url"],
		element.Tags["access"],
	}

	for i, value := range values {
		values[i] = strings.ToLower(value)
	}

	return values
}

func classifyOSMStopFeature(element overpassElement) ctdf.OSMStopFeatureType {
	switch {
	case isStopArea(element):
		return ctdf.OSMStopFeatureTypeStopArea
	case isStation(element):
		return ctdf.OSMStopFeatureTypeStation
	case isStopPosition(element):
		return ctdf.OSMStopFeatureTypeStopPosition
	case isPlatformEdge(element):
		return ctdf.OSMStopFeatureTypePlatformEdge
	case isPlatform(element):
		return ctdf.OSMStopFeatureTypePlatform
	case isEntrance(element):
		return ctdf.OSMStopFeatureTypeEntrance
	case isCafe(element):
		return ctdf.OSMStopFeatureTypeCafe
	case isRestaurant(element):
		return ctdf.OSMStopFeatureTypeRestaurant
	case isFastFood(element):
		return ctdf.OSMStopFeatureTypeFastFood
	case isPub(element):
		return ctdf.OSMStopFeatureTypePub
	case isBar(element):
		return ctdf.OSMStopFeatureTypeBar
	case isToilets(element):
		return ctdf.OSMStopFeatureTypeToilets
	case isATM(element):
		return ctdf.OSMStopFeatureTypeATM
	case isCarPark(element):
		return ctdf.OSMStopFeatureTypeCarPark
	case isBicyclePark(element):
		return ctdf.OSMStopFeatureTypeBicyclePark
	case isMotorcyclePark(element):
		return ctdf.OSMStopFeatureTypeMotorcyclePark
	case isShop(element):
		return ctdf.OSMStopFeatureTypeShop
	case isStationAmenity(element):
		return ctdf.OSMStopFeatureTypeAmenity
	case isTrack(element):
		return ctdf.OSMStopFeatureTypeTrack
	case isRoad(element):
		return ctdf.OSMStopFeatureTypeRoad
	case isAccess(element):
		return ctdf.OSMStopFeatureTypeAccess
	default:
		return ctdf.OSMStopFeatureTypeOther
	}
}

func isStopArea(element overpassElement) bool {
	return element.Type == string(ctdf.OSMElementTypeRelation) &&
		element.Tags["type"] == "public_transport" &&
		element.Tags["public_transport"] == "stop_area"
}

func isStation(element overpassElement) bool {
	return element.Tags["railway"] == "station" ||
		element.Tags["railway"] == "halt" ||
		element.Tags["public_transport"] == "station" ||
		element.Tags["amenity"] == "bus_station" ||
		element.Tags["amenity"] == "ferry_terminal"
}

func isStopPosition(element overpassElement) bool {
	return element.Tags["public_transport"] == "stop_position" || element.Tags["railway"] == "stop"
}

func isPlatform(element overpassElement) bool {
	return element.Tags["railway"] == "platform" || element.Tags["public_transport"] == "platform"
}

func isPlatformEdge(element overpassElement) bool {
	return element.Tags["railway"] == "platform_edge"
}

func isEntrance(element overpassElement) bool {
	return element.Tags["railway"] == "train_station_entrance" ||
		element.Tags["railway"] == "subway_entrance" ||
		element.Tags["entrance"] != ""
}

func isStationRetailAnchor(element overpassElement) bool {
	return isStation(element) || isStopPosition(element) || isPlatform(element) || isEntrance(element)
}

func isStationPOI(element overpassElement) bool {
	return isShop(element) || isStationAmenity(element) || isStationParking(element)
}

func isStationRetailPOI(element overpassElement) bool {
	return isShop(element) || isStationAmenity(element)
}

func hasStationContext(element overpassElement) bool {
	contextTags := []string{
		element.Tags["location"],
		element.Tags["description"],
		element.Tags["note"],
		element.Tags["addr:place"],
	}

	for _, tag := range contextTags {
		tag = strings.ToLower(tag)
		if strings.Contains(tag, "station") ||
			strings.Contains(tag, "concourse") ||
			strings.Contains(tag, "platform") ||
			strings.Contains(tag, "ticket barrier") ||
			strings.Contains(tag, "ticket barriers") {
			return true
		}
	}

	return false
}

func isStationAmenity(element overpassElement) bool {
	switch element.Tags["amenity"] {
	case "cafe", "restaurant", "fast_food", "food_court", "pub", "bar", "toilets", "atm", "bank", "pharmacy":
		return true
	default:
		return false
	}
}

func isStationParking(element overpassElement) bool {
	return isCarPark(element) || isBicyclePark(element) || isMotorcyclePark(element)
}

func isCarPark(element overpassElement) bool {
	return element.Tags["amenity"] == "parking"
}

func isBicyclePark(element overpassElement) bool {
	return element.Tags["amenity"] == "bicycle_parking"
}

func isMotorcyclePark(element overpassElement) bool {
	return element.Tags["amenity"] == "motorcycle_parking"
}

func isShop(element overpassElement) bool {
	shop := strings.ToLower(element.Tags["shop"])
	if shop == "" {
		return false
	}

	switch shop {
	case "vacant", "closed", "disused", "abandoned", "no":
		return false
	}

	return element.Tags["vacant"] != "yes" &&
		element.Tags["disused:shop"] == "" &&
		element.Tags["abandoned:shop"] == "" &&
		element.Tags["was:shop"] == ""
}

func isCafe(element overpassElement) bool {
	return element.Tags["amenity"] == "cafe"
}

func isRestaurant(element overpassElement) bool {
	return element.Tags["amenity"] == "restaurant" || element.Tags["amenity"] == "food_court"
}

func isFastFood(element overpassElement) bool {
	return element.Tags["amenity"] == "fast_food"
}

func isPub(element overpassElement) bool {
	return element.Tags["amenity"] == "pub"
}

func isBar(element overpassElement) bool {
	return element.Tags["amenity"] == "bar"
}

func isToilets(element overpassElement) bool {
	return element.Tags["amenity"] == "toilets"
}

func isATM(element overpassElement) bool {
	return element.Tags["amenity"] == "atm" || element.Tags["amenity"] == "bank"
}

func isTrack(element overpassElement) bool {
	switch element.Tags["railway"] {
	case "rail", "light_rail", "subway", "tram":
		return true
	default:
		return false
	}
}

func isRoad(element overpassElement) bool {
	return element.Tags["highway"] != "" && !isAccess(element)
}

func isAccess(element overpassElement) bool {
	switch element.Tags["highway"] {
	case "footway", "path", "steps", "pedestrian", "cycleway", "elevator":
		return true
	default:
		return false
	}
}

func isStationContainmentPolygon(element overpassElement) bool {
	if !isClosedPolygon(element.Geometry) {
		return false
	}

	return isPlatform(element) ||
		isStation(element) ||
		element.Tags["building"] != "" ||
		element.Tags["area"] == "yes" ||
		element.Tags["public_transport"] == "station"
}

func stationContainmentPolygons(element overpassElement, role string) [][]overpassPoint {
	polygons := [][]overpassPoint{}

	if isStationContainmentPolygon(element) || (isStationContainmentRole(role) && isClosedPolygon(element.Geometry)) {
		polygons = append(polygons, element.Geometry)
	}

	if element.Type == string(ctdf.OSMElementTypeRelation) &&
		(isStopArea(element) || isStation(element) || isStationContainmentRole(role)) {
		for _, member := range element.Members {
			if isClosedPolygon(member.Geometry) &&
				(member.Role == "outer" || isStationContainmentRole(member.Role)) {
				polygons = append(polygons, member.Geometry)
			}
		}
	}

	return polygons
}

func stationContainmentMemberPolygons(member overpassMember) [][]overpassPoint {
	if !isStationContainmentRole(member.Role) || !isClosedPolygon(member.Geometry) {
		return nil
	}

	return [][]overpassPoint{member.Geometry}
}

func isStationContainmentRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "station", "platform", "entrance", "building", "outline":
		return true
	default:
		return false
	}
}

func elementInsideAnyPolygon(element overpassElement, polygons [][]overpassPoint) bool {
	if len(polygons) == 0 {
		return false
	}

	point, ok := representativePoint(element)
	if !ok {
		return false
	}

	for _, polygon := range polygons {
		if pointInPolygon(point, polygon) {
			return true
		}
	}

	return false
}

func elementNearAnyPoint(element overpassElement, points []overpassPoint, maxDistanceMetres float64) bool {
	distanceMetres, ok := nearestElementDistanceToPoints(element, points)
	if !ok {
		return false
	}

	return distanceMetres <= maxDistanceMetres
}

func nearestElementDistanceToPoints(element overpassElement, points []overpassPoint) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}

	point, ok := representativePoint(element)
	if !ok {
		return 0, false
	}

	nearestDistanceMetres := math.MaxFloat64
	for _, candidate := range points {
		distanceMetres := haversineMetres(point.Lat, point.Lon, candidate.Lat, candidate.Lon)
		if distanceMetres < nearestDistanceMetres {
			nearestDistanceMetres = distanceMetres
		}
	}

	return nearestDistanceMetres, true
}

func anchorPoints(element overpassElement) []overpassPoint {
	points := make([]overpassPoint, 0, len(element.Geometry)+2)

	if element.Type == string(ctdf.OSMElementTypeNode) {
		points = append(points, overpassPoint{Lat: element.Lat, Lon: element.Lon})
	}
	if element.Center != nil {
		points = append(points, *element.Center)
	}
	points = append(points, element.Geometry...)

	if len(points) == 0 {
		if point, ok := representativePoint(element); ok {
			points = append(points, point)
		}
	}

	return points
}

func isClosedPolygon(points []overpassPoint) bool {
	if len(points) < 4 {
		return false
	}

	first := points[0]
	last := points[len(points)-1]

	return first.Lat == last.Lat && first.Lon == last.Lon
}

func pointInPolygon(point overpassPoint, polygon []overpassPoint) bool {
	inside := false
	j := len(polygon) - 1

	for i := 0; i < len(polygon); i++ {
		xi := polygon[i].Lon
		yi := polygon[i].Lat
		xj := polygon[j].Lon
		yj := polygon[j].Lat

		intersects := ((yi > point.Lat) != (yj > point.Lat)) &&
			(point.Lon < (xj-xi)*(point.Lat-yi)/(yj-yi)+xi)
		if intersects {
			inside = !inside
		}

		j = i
	}

	return inside
}

func representativePoint(element overpassElement) (overpassPoint, bool) {
	if element.Type == string(ctdf.OSMElementTypeNode) {
		return overpassPoint{Lat: element.Lat, Lon: element.Lon}, true
	}
	if element.Center != nil {
		return *element.Center, true
	}
	if len(element.Geometry) > 0 {
		return element.Geometry[len(element.Geometry)/2], true
	}
	if element.Bounds != nil {
		return overpassPoint{
			Lat: (element.Bounds.MinLat + element.Bounds.MaxLat) / 2,
			Lon: (element.Bounds.MinLon + element.Bounds.MaxLon) / 2,
		}, true
	}

	return overpassPoint{}, false
}

func overpassPointToLocation(point overpassPoint) *ctdf.Location {
	return &ctdf.Location{
		Type:        "Point",
		Coordinates: []float64{point.Lon, point.Lat},
	}
}

func overpassGeometryToLocations(points []overpassPoint) []ctdf.Location {
	locations := make([]ctdf.Location, 0, len(points))
	for _, point := range points {
		locations = append(locations, ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{point.Lon, point.Lat},
		})
	}

	return locations
}

func osmElementRef(element overpassElement) ctdf.OSMElementRef {
	return ctdf.OSMElementRef{
		Type: ctdf.OSMElementType(element.Type),
		ID:   element.ID,
	}
}

func overpassElementKey(element overpassElement) string {
	return overpassElementRefKey(element.Type, element.ID)
}

func overpassElementRefKey(elementType string, id int64) string {
	return elementType + "/" + strconv.FormatInt(id, 10)
}

func mapOverpassElementsByKey(elements []overpassElement) map[string]overpassElement {
	byKey := make(map[string]overpassElement, len(elements))
	for _, element := range elements {
		byKey[overpassElementKey(element)] = element
	}

	return byKey
}

func identifierValues(stop *ctdf.Stop, prefix string) []string {
	values := make([]string, 0, len(stop.GetAllStopIDs()))
	for _, id := range stop.GetAllStopIDs() {
		if strings.HasPrefix(id, prefix) {
			values = append(values, strings.TrimPrefix(id, prefix))
		}
	}
	return values
}

func firstValue(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

func bestIdentifierMatch(crs string, tiploc string, atco string, gtfsStopID string) (ctdf.OSMStopMatchMethod, string) {
	switch {
	case crs != "":
		return ctdf.OSMStopMatchMethodCRS, crs
	case tiploc != "":
		return ctdf.OSMStopMatchMethodTIPLOC, tiploc
	case atco != "":
		return ctdf.OSMStopMatchMethodNaPTAN, atco
	case gtfsStopID != "":
		return ctdf.OSMStopMatchMethodGTFS, gtfsStopID
	default:
		return ctdf.OSMStopMatchMethodUnspecified, ""
	}
}

func defaultRadius(stop *ctdf.Stop) int {
	for _, transportType := range stop.TransportTypes {
		switch transportType {
		case ctdf.TransportTypeRail, ctdf.TransportTypeMetro:
			return defaultRailSearchRadius
		case ctdf.TransportTypeBus, ctdf.TransportTypeCoach:
			return defaultBusSearchRadius
		}
	}

	return defaultOtherSearchRadius
}

func osmStopMatchConfidence(method ctdf.OSMStopMatchMethod, stopArea *overpassElement) float64 {
	if stopArea == nil {
		return 0.4
	}

	switch method {
	case ctdf.OSMStopMatchMethodCRS, ctdf.OSMStopMatchMethodNaPTAN, ctdf.OSMStopMatchMethodTIPLOC, ctdf.OSMStopMatchMethodGTFS:
		return 0.95
	case ctdf.OSMStopMatchMethodCoordinate:
		return 0.7
	case ctdf.OSMStopMatchMethodManual, ctdf.OSMStopMatchMethodRelationID:
		return 1
	default:
		return 0.5
	}
}

func osmStopDistanceToSelection(stop *ctdf.Stop, stopArea *overpassElement, station *overpassElement) float64 {
	if stop.Location == nil {
		return 0
	}

	var selected *overpassElement
	if stopArea != nil {
		selected = stopArea
	} else if station != nil {
		selected = station
	}
	if selected == nil {
		return 0
	}

	point, ok := representativePoint(*selected)
	if !ok {
		return 0
	}

	return haversineMetres(stop.Location.Coordinates[1], stop.Location.Coordinates[0], point.Lat, point.Lon)
}

func haversineMetres(lat1 float64, lon1 float64, lat2 float64, lon2 float64) float64 {
	const earthRadiusMetres = 6371000
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusMetres * c
}
