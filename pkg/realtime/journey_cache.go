package realtime

import "github.com/britbus/britbus/pkg/ctdf"

type CacheJourney struct {
	Path []*ctdf.JourneyPathItem `groups:"detailed"`
}
