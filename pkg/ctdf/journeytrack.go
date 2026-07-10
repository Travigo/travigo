package ctdf

import (
	"context"
	"time"

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

func (j *Journey) GetTracks() {
	if j == nil {
		return
	}
	refs := make([]string, 0, len(j.Path)+1)
	if j.TrackRef != "" {
		refs = append(refs, j.TrackRef)
	}
	for _, path := range j.Path {
		if path != nil && path.TrackRef != "" {
			refs = append(refs, path.TrackRef)
		}
	}
	if len(refs) == 0 {
		return
	}
	cursor, err := database.GetCollection("journey_tracks").Find(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": refs}})
	if err != nil {
		return
	}
	defer cursor.Close(context.Background())
	tracks := map[string][]Location{}
	for cursor.Next(context.Background()) {
		var track JourneyTrack
		if cursor.Decode(&track) == nil {
			tracks[track.PrimaryIdentifier] = track.Track
		}
	}
	if j.TrackRef != "" {
		j.Track = tracks[j.TrackRef]
	}
	for _, path := range j.Path {
		if path != nil && path.TrackRef != "" {
			path.Track = tracks[path.TrackRef]
		}
	}
}
