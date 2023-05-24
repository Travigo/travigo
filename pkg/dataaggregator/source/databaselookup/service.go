package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) ServiceQuery(q ctdf.QueryService) (*ctdf.Service, error) {
	collection := database.GetCollection("services")
	var service *ctdf.Service
	collection.FindOne(context.Background(), q.ToBson()).Decode(&service)

	if service == nil {
		return nil, errors.New("could not find a matching Service")
	} else {
		return service, nil
	}
}
