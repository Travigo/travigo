package gtfs

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestAppendServiceRouteKeepsDistinctNamedEndpoints(t *testing.T) {
	service := &ctdf.Service{}
	seen := map[routeEndpointPair]struct{}{}

	appendServiceRoute(service, seen, "Bournemouth", "Wimborne")
	appendServiceRoute(service, seen, "Bournemouth", "Wimborne")
	appendServiceRoute(service, seen, "Wimborne", "Bournemouth")
	appendServiceRoute(service, seen, "", "Bournemouth")

	if len(service.Routes) != 2 {
		t.Fatalf("routes = %+v, want two distinct directional endpoint pairs", service.Routes)
	}
	if service.Routes[0].Origin != "Bournemouth" || service.Routes[0].Destination != "Wimborne" {
		t.Fatalf("first route = %+v", service.Routes[0])
	}
	if service.Routes[1].Origin != "Wimborne" || service.Routes[1].Destination != "Bournemouth" {
		t.Fatalf("second route = %+v", service.Routes[1])
	}
}
