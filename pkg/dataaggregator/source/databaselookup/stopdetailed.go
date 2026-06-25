package databaselookup

import (
	"slices"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/util"
)

func (s Source) StopDetailedQuery(stopQuery query.StopDetailed) (*ctdf.StopDetailed, error) {
	stop, err := s.Lookup(query.Stop{Identifier: stopQuery.PrimaryIdentifier})
	if err != nil {
		return nil, err
	}

	osmStop, err := s.Lookup(query.OSMStop{Stop: stop.(*ctdf.Stop)})
	if err != nil {
		return nil, err
	}

	// Build detailed stop information from OSM features
	stopDetailed := &ctdf.StopDetailed{
		FoodDrink: []ctdf.StopShop{},
		Shops:     []ctdf.StopShop{},
	}

	for _, feature := range osmStop.(*ctdf.OSMStop).Features {
		if isRelevantFeatureType(feature.Type) {
			if isShopFeatureType(feature.Type) || isFoodDrinkFeatureType(feature.Type) {
				wikiDataID := feature.Tags["wikidata"]
				if wikiDataID == "" {
					wikiDataID = feature.Tags["brand:wikidata"]
				}

				shopType := feature.Tags["shop"]
				if shopType == "" {
					shopType = feature.Tags["amenity"]
				}

				if feature.Tags["cuisine"] != "" {
					cuisineSplit := util.SplitAndTrim(feature.Tags["cuisine"], ";")

					cuisineSplit = slices.DeleteFunc(cuisineSplit, func(cuisine string) bool {
						return cuisine == "" || cuisine == shopType
					})

					for i, cuisine := range cuisineSplit {
						cuisineSplit[i] = strings.ReplaceAll(cuisine, "_", " ")
					}

					if len(cuisineSplit) > 1 {
						shopType = shopType + " - " + strings.Join(cuisineSplit, ", ")
					}
				}

				stopShop := ctdf.StopShop{
					PrimaryName:         feature.PrimaryName,
					Type:                strings.Title(shopType),
					Website:             feature.Tags["website"],
					WikiDataID:          wikiDataID,
					LocationDescription: feature.Tags["location"],
					Location:            feature.Location,
				}
				if isFoodDrinkFeatureType(feature.Type) {
					stopDetailed.FoodDrink = append(stopDetailed.FoodDrink, stopShop)
				} else {
					stopDetailed.Shops = append(stopDetailed.Shops, stopShop)
				}
			} else if isToiletsFeatureType(feature.Type) {
				stopToilets := ctdf.StopToilets{
					CustomerOnly:        feature.Tags["access"] == "customers",
					Cost:                feature.Tags["fee"] == "yes",
					Accessible:          feature.Tags["wheelchair"] == "yes",
					Location:            feature.Location,
					LocationDescription: feature.Tags["note"],
				}
				stopDetailed.Toilets = append(stopDetailed.Toilets, stopToilets)
			}
		}
	}
	return stopDetailed, nil
}

func isFoodDrinkFeatureType(featureType ctdf.OSMStopFeatureType) bool {
	switch featureType {
	case ctdf.OSMStopFeatureTypeCafe,
		ctdf.OSMStopFeatureTypeRestaurant,
		ctdf.OSMStopFeatureTypeFastFood,
		ctdf.OSMStopFeatureTypePub,
		ctdf.OSMStopFeatureTypeBar:
		return true
	default:
		return false
	}
}

func isShopFeatureType(featureType ctdf.OSMStopFeatureType) bool {
	return featureType == ctdf.OSMStopFeatureTypeShop
}

func isToiletsFeatureType(featureType ctdf.OSMStopFeatureType) bool {
	return featureType == ctdf.OSMStopFeatureTypeToilets
}

func isRelevantFeatureType(featureType ctdf.OSMStopFeatureType) bool {
	if isFoodDrinkFeatureType(featureType) {
		return true
	}
	if isShopFeatureType(featureType) {
		return true
	}
	if isToiletsFeatureType(featureType) {
		return true
	}

	switch featureType {
	case ctdf.OSMStopFeatureTypeAmenity,
		ctdf.OSMStopFeatureTypeATM:
		return true
	default:
		return false
	}

}
