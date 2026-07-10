package ctdf

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type JourneyTrack struct {
	PrimaryIdentifier    string               `groups:"internal,detailed" bson:",omitempty"`
	Track                []Location           `groups:"detailed" bson:",omitempty"`
	DataSource           *DataSourceReference `groups:"internal" bson:",omitempty"`
	CreationDateTime     time.Time            `groups:"internal" bson:",omitempty"`
	ModificationDateTime time.Time            `groups:"internal" bson:",omitempty"`
}

type JourneyTrackHydrationStats struct {
	References int
	Hydrated   int
	Missing    int
	QueryError bool
}

func (j *Journey) GetTracks() JourneyTrackHydrationStats {
	stats := JourneyTrackHydrationStats{}
	if j == nil {
		return stats
	}
	refs := make([]string, 0, len(j.Path)+1)
	seenRefs := make(map[string]struct{}, len(j.Path)+1)
	if j.TrackRef != "" {
		seenRefs[j.TrackRef] = struct{}{}
		refs = append(refs, j.TrackRef)
	}
	for _, path := range j.Path {
		if path != nil && path.TrackRef != "" {
			if _, seen := seenRefs[path.TrackRef]; !seen {
				seenRefs[path.TrackRef] = struct{}{}
				refs = append(refs, path.TrackRef)
			}
		}
	}
	stats.References = len(refs)
	if len(refs) == 0 {
		return stats
	}
	cursor, err := database.GetCollection("journey_tracks").Find(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": refs}})
	if err != nil {
		stats.QueryError = true
		log.Warn().Err(err).Str("journey", j.PrimaryIdentifier).Int("track_references", stats.References).Msg("Failed to hydrate journey tracks")
		return stats
	}
	defer cursor.Close(context.Background())
	tracks := map[string][]Location{}
	for cursor.Next(context.Background()) {
		var track JourneyTrack
		if cursor.Decode(&track) == nil {
			tracks[track.PrimaryIdentifier] = track.Track
		}
	}
	if err := cursor.Err(); err != nil {
		stats.QueryError = true
		log.Warn().Err(err).Str("journey", j.PrimaryIdentifier).Msg("Failed while hydrating journey tracks")
		return stats
	}
	stats.Hydrated = len(tracks)
	stats.Missing = stats.References - stats.Hydrated
	if j.TrackRef != "" {
		j.Track = tracks[j.TrackRef]
	}
	for _, path := range j.Path {
		if path != nil && path.TrackRef != "" {
			path.Track = tracks[path.TrackRef]
		}
	}
	if stats.Missing > 0 {
		log.Warn().Str("journey", j.PrimaryIdentifier).Int("track_references", stats.References).Int("hydrated_tracks", stats.Hydrated).Int("missing_tracks", stats.Missing).Msg("Journey track references are missing")
	}
	return stats
}
