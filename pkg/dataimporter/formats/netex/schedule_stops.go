package netex

import (
	"encoding/xml"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

type stopData struct {
	scheduledStopPoints  map[string]*xmlNode
	passengerAssignments map[string]passengerStopAssignment
	stopPlaces           map[string]*xmlNode
	quays                map[string]*xmlNode
	placeParents         map[string]string
	quayParents          map[string]string
}

type passengerStopAssignment struct {
	stopPointRef string
	stopPlaceRef string
	quayRef      string
}

func (s *stopData) ensureStopMaps() {
	if s.scheduledStopPoints == nil {
		s.scheduledStopPoints = make(map[string]*xmlNode)
		s.passengerAssignments = make(map[string]passengerStopAssignment)
		s.stopPlaces = make(map[string]*xmlNode)
		s.quays = make(map[string]*xmlNode)
		s.placeParents = make(map[string]string)
		s.quayParents = make(map[string]string)
	}
}

func (s *stopData) decodeStopElement(decoder *xml.Decoder, start xml.StartElement) (bool, error) {
	switch start.Name.Local {
	case "ScheduledStopPoint", "PassengerStopAssignment", "StopPlace", "Quay":
		var node xmlNode
		if err := decoder.DecodeElement(&node, &start); err != nil {
			return true, err
		}
		s.ensureStopMaps()
		id := nodeAttr(&node, "id")
		switch start.Name.Local {
		case "ScheduledStopPoint":
			if id != "" {
				s.scheduledStopPoints[id] = &node
			}
		case "PassengerStopAssignment":
			pointRef := refFrom(&node, "ScheduledStopPointRef")
			if pointRef != "" {
				assignment := passengerStopAssignment{
					stopPointRef: pointRef,
					stopPlaceRef: refFrom(&node, "StopPlaceRef"),
					quayRef:      refFrom(&node, "QuayRef"),
				}
				if previous, exists := s.passengerAssignments[pointRef]; exists && previous != assignment {
					return true, fmt.Errorf("ScheduledStopPoint %q has conflicting PassengerStopAssignments (%q/%q and %q/%q)", pointRef, previous.quayRef, previous.stopPlaceRef, assignment.quayRef, assignment.stopPlaceRef)
				}
				s.passengerAssignments[pointRef] = assignment
			}
		case "StopPlace":
			if id != "" {
				s.stopPlaces[id] = &node
				if parent := firstNonEmpty(refFrom(&node, "ParentSiteRef"), refFrom(&node, "ParentZoneRef")); parent != "" {
					s.placeParents[id] = parent
				}
				indexNestedStopObjects(s, &node, id)
			}
		case "Quay":
			if id != "" {
				s.quays[id] = &node
				if parent := firstNonEmpty(refFrom(&node, "ParentZoneRef"), refFrom(&node, "ParentSiteRef")); parent != "" {
					s.quayParents[id] = parent
				}
			}
		}
		return true, nil
	default:
		return false, nil
	}
}

func indexNestedStopObjects(data *stopData, parent *xmlNode, parentRef string) {
	for _, child := range parent.Children {
		switch child.Name.Local {
		case "StopPlace":
			if id := nodeAttr(child, "id"); id != "" {
				data.stopPlaces[id] = child
				data.placeParents[id] = idParent(child, parentRef)
				indexNestedStopObjects(data, child, id)
			}
		case "Quay":
			if id := nodeAttr(child, "id"); id != "" {
				data.quays[id] = child
				data.quayParents[id] = firstNonEmpty(refFrom(child, "ParentZoneRef"), refFrom(child, "ParentSiteRef"), parentRef)
			}
		default:
			indexNestedStopObjects(data, child, parentRef)
		}
	}
}

func idParent(node *xmlNode, implicit string) string {
	return firstNonEmpty(refFrom(node, "ParentSiteRef"), refFrom(node, "ParentZoneRef"), implicit)
}

func refFrom(node *xmlNode, name string) string {
	return nodeAttr(firstNode(node, name), "ref")
}

func (s *Schedule) convertStops(dataset datasets.DataSet, source *ctdf.DataSourceReference, now time.Time) ([]*ctdf.Stop, []*ctdf.StopGroup, error) {
	stops := make([]*ctdf.Stop, 0, len(s.stopPlaces)+len(s.quays))
	groups := make([]*ctdf.StopGroup, 0, len(s.stopPlaces))
	publicCodes := stopPublicCodeCounts(s.stopPlaces, s.quays)
	refs := make([]string, 0, len(s.stopPlaces))
	for ref := range s.stopPlaces {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		node := s.stopPlaces[ref]
		name := nodeText(node, "Name")
		stop := &ctdf.Stop{
			PrimaryIdentifier:    netexStopIdentifier(dataset.Identifier, ref),
			OtherIdentifiers:     netexPhysicalIdentifiers(dataset.Identifier, node, publicCodes),
			CreationDateTime:     now,
			ModificationDateTime: now,
			DataSource:           source,
			PrimaryName:          name,
			LocationType:         nodeText(node, "StopPlaceType"),
			Active:               true,
		}
		if location := netexLocation(node); location != nil {
			stop.Location = location
		}
		if parent := firstNonEmpty(refFrom(node, "ParentSiteRef"), refFrom(node, "ParentZoneRef"), s.placeParents[ref]); parent != "" {
			stop.Associations = append(stop.Associations, &ctdf.Association{Type: "parent", AssociatedIdentifier: netexStopIdentifier(dataset.Identifier, parent)})
		}
		stops = append(stops, stop)
		groups = append(groups, &ctdf.StopGroup{
			PrimaryIdentifier:    netexStopGroupIdentifier(dataset.Identifier, ref),
			OtherIdentifiers:     netexPhysicalIdentifiers(dataset.Identifier, node, publicCodes),
			CreationDateTime:     now,
			ModificationDateTime: now,
			DataSource:           source,
			Name:                 name,
			Type:                 nodeText(node, "StopPlaceType"),
			Status:               "active",
		})
	}
	refs = refs[:0]
	for ref := range s.quays {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		node := s.quays[ref]
		parent := firstNonEmpty(refFrom(node, "ParentZoneRef"), refFrom(node, "ParentSiteRef"), s.quayParents[ref])
		stop := &ctdf.Stop{
			PrimaryIdentifier:    netexStopIdentifier(dataset.Identifier, ref),
			OtherIdentifiers:     netexPhysicalIdentifiers(dataset.Identifier, node, publicCodes),
			CreationDateTime:     now,
			ModificationDateTime: now,
			DataSource:           source,
			PrimaryName:          nodeText(node, "Name"),
			LocationType:         "quay",
			PlatformCode:         nodeText(node, "PublicCode"),
			Active:               true,
		}
		if location := netexLocation(node); location != nil {
			stop.Location = location
		}
		if parent != "" {
			if place := s.stopPlaces[parent]; place != nil {
				stop.PrimaryName = firstNonEmpty(stop.PrimaryName, nodeText(place, "Name"))
				if stop.Location == nil {
					stop.Location = netexLocation(place)
				}
			}
			stop.Associations = append(stop.Associations, &ctdf.Association{Type: "parent", AssociatedIdentifier: netexStopIdentifier(dataset.Identifier, parent)})
			stop.Associations = append(stop.Associations, &ctdf.Association{Type: "stop_group", AssociatedIdentifier: netexStopGroupIdentifier(dataset.Identifier, parent)})
		}
		stops = append(stops, stop)
	}
	return stops, groups, nil
}

// resolveStop maps a NeTEx object reference (or ScheduledStopPoint reference)
// to the identifier expected by a schedule. It deliberately performs no DB IO.
func (s *Schedule) resolveStop(dataset datasets.DataSet, ref string) (id, name string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty NeTEx stop reference")
	}
	s.ensureStopMaps()
	scheduledName := nodeText(s.scheduledStopPoints[ref], "Name")
	assignment := s.passengerAssignments[ref]
	assignedRef := firstNonEmpty(assignment.quayRef, assignment.stopPlaceRef)
	if assignedRef != "" {
		ref = assignedRef
	}
	node := s.quays[ref]
	if node == nil {
		node = s.stopPlaces[ref]
	}
	scope := dataset.Identifier
	linkedScope := dataset.LinkedDataset != "" && !dataset.SupportedObjects.Stops
	if linkedScope {
		scope = dataset.LinkedDataset
	}
	if node == nil && (!linkedScope || assignedRef == "") {
		return "", "", nil
	}
	parent := s.stopPlaces[s.quayParents[ref]]
	return netexStopIdentifier(scope, ref), firstNonEmpty(nodeText(node, "Name"), nodeText(parent, "Name"), scheduledName), nil
}

func netexStopIdentifier(datasetID, ref string) string {
	return fmt.Sprintf("%s-stop-%s", datasetID, ref)
}

func netexStopGroupIdentifier(datasetID, ref string) string {
	return fmt.Sprintf("%s-stopgroup-%s", datasetID, ref)
}

func stopPublicCodeCounts(stopPlaces, quays map[string]*xmlNode) map[string]int {
	counts := make(map[string]int)
	for _, nodes := range []map[string]*xmlNode{stopPlaces, quays} {
		for _, node := range nodes {
			for _, name := range []string{"PublicCode", "PrivateCode", "ShortName"} {
				if value := nodeText(node, name); value != "" {
					counts[value]++
				}
			}
			for _, keyValue := range findNodes(firstNode(node, "KeyList"), "KeyValue") {
				if value := nodeText(keyValue, "Value"); value != "" {
					counts[value]++
				}
			}
		}
	}
	return counts
}

func netexPhysicalIdentifiers(datasetID string, node *xmlNode, uniqueValues map[string]int) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		id := netexStopIdentifier(datasetID, value)
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	for _, name := range []string{"PublicCode", "PrivateCode", "ShortName"} {
		if value := nodeText(node, name); value != "" && uniqueValues[value] == 1 {
			add(value)
		}
	}
	for _, keyValue := range findNodes(firstNode(node, "KeyList"), "KeyValue") {
		key := strings.ToLower(nodeText(keyValue, "Key"))
		value := nodeText(keyValue, "Value")
		if value == "" || uniqueValues[value] != 1 {
			continue
		}
		if strings.Contains(key, "atco") || strings.Contains(key, "naptan") {
			id := fmt.Sprintf(ctdf.GBStopIDFormat, value)
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		} else {
			add(value)
		}
	}
	return result
}

func netexLocation(node *xmlNode) *ctdf.Location {
	location := firstNode(node, "Location")
	if centroid := firstNode(node, "Centroid"); centroid != nil {
		location = firstNode(centroid, "Location")
	}
	longitude, errLon := strconv.ParseFloat(nodeText(location, "Longitude"), 64)
	latitude, errLat := strconv.ParseFloat(nodeText(location, "Latitude"), 64)
	if errLon != nil || errLat != nil || math.IsNaN(longitude) || math.IsInf(longitude, 0) || math.IsNaN(latitude) || math.IsInf(latitude, 0) || longitude < -180 || longitude > 180 || latitude < -90 || latitude > 90 {
		return nil
	}
	return &ctdf.Location{Type: "Point", Coordinates: []float64{longitude, latitude}}
}
