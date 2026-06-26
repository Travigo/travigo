package databaselookup

import (
	"slices"
	"strconv"
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
		FoodDrink:   []ctdf.StopShop{},
		Shops:       []ctdf.StopShop{},
		CarPark:     []ctdf.StopParking{},
		BicyclePark: []ctdf.StopParking{},
	}

	for _, feature := range osmStop.(*ctdf.OSMStop).Features {
		if isRelevantFeatureType(feature.Type) {

			// Shop & Food/Drink
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

					if len(cuisineSplit) > 1 {
						shopType = shopType + " - " + strings.Join(cuisineSplit, ", ")
					}
				}

				stopShop := ctdf.StopShop{
					PrimaryName:         feature.PrimaryName,
					Type:                strings.Title(strings.ReplaceAll(shopType, "_", " ")),
					Website:             feature.Tags["website"],
					WikiDataID:          wikiDataID,
					LocationDescription: feature.Tags["location"],
					Association:         string(feature.Association),
					DistanceMetres:      feature.DistanceMetres,
					Location:            feature.Location,
				}
				if isFoodDrinkFeatureType(feature.Type) {
					stopDetailed.FoodDrink = append(stopDetailed.FoodDrink, stopShop)
				} else {
					stopDetailed.Shops = append(stopDetailed.Shops, stopShop)
				}
			} else if isToiletsFeatureType(feature.Type) {
				stopToilets := ctdf.StopToilets{
					CustomerOnly:         feature.Tags["access"] == "customers",
					Cost:                 feature.Tags["fee"] == "yes",
					Accessible:           feature.Tags["wheelchair"] == "yes",
					Male:                 feature.Tags["male"] == "yes",
					Female:               feature.Tags["female"] == "yes",
					OpenHoursDescription: feature.Tags["opening_hours"],
					Location:             feature.Location,
					LocationDescription:  feature.Tags["note"],
					Association:          string(feature.Association),
					DistanceMetres:       feature.DistanceMetres,
				}
				stopDetailed.Toilets = append(stopDetailed.Toilets, stopToilets)
			} else if isParkingFeatureType(feature.Type) {
				capacity, _ := strconv.Atoi(feature.Tags["capacity"])

				parkingType := feature.Tags["parking"]
				if feature.Tags["bicycle_parking"] != "" {
					parkingType = feature.Tags["bicycle_parking"]
				}

				stopParking := ctdf.StopParking{
					PrimaryName:        feature.Tags["name"],
					Type:               parkingType,
					Cost:               feature.Tags["fee"] == "yes",
					Accessible:         feature.Tags["wheelchair"] == "yes",
					OperatorName:       feature.Tags["operator"],
					OperatorWikiDataID: feature.Tags["operator:wikidata"],
					Association:        string(feature.Association),
					DistanceMetres:     feature.DistanceMetres,
					Capacity:           capacity,
					Covered:            feature.Tags["covered"] == "yes",
				}

				if feature.Type == ctdf.OSMStopFeatureTypeCarPark && feature.ParkingAssociation == ctdf.OSMStopParkingOfficial {
					stopDetailed.CarPark = append(stopDetailed.CarPark, stopParking)
				} else if feature.Type == ctdf.OSMStopFeatureTypeBicyclePark {
					stopDetailed.BicyclePark = append(stopDetailed.BicyclePark, stopParking)
				}
			}
		}
	}
	stopDetailed.BicyclePark = mergeStopBicycleParking(stopDetailed.BicyclePark)
	return stopDetailed, nil
}

type stopBicycleParkingMergeKey struct {
	cost        bool
	covered     bool
	parkingType string
	accessible  bool
	primaryName string
}

func mergeStopBicycleParking(parking []ctdf.StopParking) []ctdf.StopParking {
	merged := []ctdf.StopParking{}
	mergedIndexByKey := map[stopBicycleParkingMergeKey]int{}

	for _, item := range parking {
		key := stopBicycleParkingMergeKey{
			cost:        item.Cost,
			covered:     item.Covered,
			parkingType: item.Type,
			accessible:  item.Accessible,
			primaryName: item.PrimaryName,
		}

		index, exists := mergedIndexByKey[key]
		if !exists {
			mergedIndexByKey[key] = len(merged)
			merged = append(merged, item)
			continue
		}

		merged[index].Capacity += item.Capacity
		if merged[index].DistanceMetres == 0 ||
			(item.DistanceMetres > 0 && item.DistanceMetres < merged[index].DistanceMetres) {
			merged[index].DistanceMetres = item.DistanceMetres
		}
		if merged[index].Association == "" {
			merged[index].Association = item.Association
		}
		if merged[index].OperatorName == "" {
			merged[index].OperatorName = item.OperatorName
		}
		if merged[index].OperatorWikiDataID == "" {
			merged[index].OperatorWikiDataID = item.OperatorWikiDataID
		}
	}

	return merged
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

func isParkingFeatureType(featureType ctdf.OSMStopFeatureType) bool {
	return featureType == ctdf.OSMStopFeatureTypeCarPark || featureType == ctdf.OSMStopFeatureTypeBicyclePark
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
	if isParkingFeatureType(featureType) {
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
