package datasets

import (
	"path"
	"slices"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Overrides struct {
	OperatorAliases  []OperatorAlias
	OperatorMetadata []OperatorMetadata
	ServiceMetadata  []ServiceMetadata
	ServiceBranding  []ServiceBranding
}

type OperatorAlias struct {
	Datasets []string // Matches identifiers as written in this datasource.
	From     []string
	To       string
}

// OperatorMetadata applies reviewed passenger contact corrections to an exact
// operator record from a selected source dataset.
type OperatorMetadata struct {
	Datasets  []string // Matches identifiers as written in this datasource.
	Operators []string
	Set       struct {
		PrimaryName *string
		Website     *string
		Email       *string
		Address     *string
		PhoneNumber *string
		OtherNames  *[]string
		SocialMedia *map[string]string
		Regions     *[]string
	}
}

func (r OperatorMetadata) Matches(dataset, operatorID string) bool {
	return matchesDataset(r.Datasets, dataset) && slices.Contains(r.Operators, operatorID)
}

func (r OperatorMetadata) Apply(operator *ctdf.Operator) {
	if operator == nil {
		return
	}
	if r.Set.PrimaryName != nil {
		operator.PrimaryName = *r.Set.PrimaryName
	}
	if r.Set.Website != nil {
		operator.Website = *r.Set.Website
	}
	if r.Set.Email != nil {
		operator.Email = *r.Set.Email
	}
	if r.Set.Address != nil {
		operator.Address = *r.Set.Address
	}
	if r.Set.PhoneNumber != nil {
		operator.PhoneNumber = *r.Set.PhoneNumber
	}
	if r.Set.OtherNames != nil {
		operator.OtherNames = *r.Set.OtherNames
	}
	if r.Set.SocialMedia != nil {
		operator.SocialMedia = *r.Set.SocialMedia
	}
	if r.Set.Regions != nil {
		operator.Regions = *r.Set.Regions
	}
}

func (o Overrides) OperatorMetadataFor(dataset string) []OperatorMetadata {
	var rules []OperatorMetadata
	for _, rule := range o.OperatorMetadata {
		if matchesDataset(rule.Datasets, dataset) {
			rules = append(rules, rule)
		}
	}
	return rules
}

// ServiceMetadata applies reviewed passenger-facing text and timetable links
// to services from an exact source dataset, operator and service number.
type ServiceMetadata struct {
	Datasets       []string
	Operators      []string
	Services       []string
	TransportTypes []ctdf.TransportType
	Set            struct {
		ServiceName *string
		Description *string
		Website     *string
		Routes      *[]ctdf.Route
	}
}

func (r ServiceMetadata) Matches(service *ctdf.Service) bool {
	if !slices.Contains(r.Operators, service.OperatorRef) ||
		(len(r.Services) > 0 && !slices.Contains(r.Services, service.ServiceName)) ||
		(len(r.TransportTypes) > 0 && !slices.Contains(r.TransportTypes, service.TransportType)) {
		return false
	}
	if len(r.Datasets) > 0 && (service.DataSource == nil || !matchesDataset(r.Datasets, service.DataSource.DatasetID)) {
		return false
	}
	return true
}

func (r ServiceMetadata) apply(service *ctdf.Service) {
	if r.Set.ServiceName != nil {
		service.ServiceName = *r.Set.ServiceName
	}
	if r.Set.Description != nil {
		service.Description = *r.Set.Description
	}
	if r.Set.Website != nil {
		service.Website = *r.Set.Website
	}
	if r.Set.Routes != nil {
		routeIndexes := make(map[[2]string]int, len(service.Routes)+len(*r.Set.Routes))
		for index, route := range service.Routes {
			routeIndexes[[2]string{route.Origin, route.Destination}] = index
		}
		for _, route := range *r.Set.Routes {
			key := [2]string{route.Origin, route.Destination}
			if index, exists := routeIndexes[key]; exists {
				if route.Description != "" {
					service.Routes[index].Description = route.Description
				}
				continue
			}
			routeIndexes[key] = len(service.Routes)
			service.Routes = append(service.Routes, route)
		}
	}
}

func (o Overrides) AliasesFor(dataset string) map[string]string {
	var aliases map[string]string
	for _, rule := range o.OperatorAliases {
		if !matchesDataset(rule.Datasets, dataset) {
			continue
		}
		if aliases == nil {
			aliases = make(map[string]string)
		}
		for _, ref := range rule.From {
			aliases[ref] = rule.To
		}
	}
	return aliases
}

type ServiceBranding struct {
	Datasets       []string // Matches fully qualified dataset IDs. Empty applies across datasources.
	Operators      []string
	Services       []string
	TransportTypes []ctdf.TransportType
	Fallback       bool // When true, only fills brand fields that the publisher left empty.
	Set            struct {
		Colour          *string
		SecondaryColour *string
		Icon            *string
		DisplayMode     *string
	}
}

func (r ServiceBranding) Matches(service *ctdf.Service) bool {
	if !slices.Contains(r.Operators, service.OperatorRef) ||
		(len(r.Services) > 0 && !slices.Contains(r.Services, service.ServiceName)) ||
		(len(r.TransportTypes) > 0 && !slices.Contains(r.TransportTypes, service.TransportType)) {
		return false
	}

	if len(r.Datasets) > 0 && (service.DataSource == nil || !matchesDataset(r.Datasets, service.DataSource.DatasetID)) {
		return false
	}
	return true
}

func (r ServiceBranding) apply(service *ctdf.Service) {
	if r.Set.Colour != nil && (!r.Fallback || service.BrandColour == "") {
		service.BrandColour = *r.Set.Colour
	}
	if r.Set.SecondaryColour != nil && (!r.Fallback || service.SecondaryBrandColour == "") {
		service.SecondaryBrandColour = *r.Set.SecondaryColour
	}
	if r.Set.Icon != nil && (!r.Fallback || service.BrandIcon == "") {
		service.BrandIcon = *r.Set.Icon
	}
	if r.Set.DisplayMode != nil && (!r.Fallback || service.BrandDisplayMode == "") {
		service.BrandDisplayMode = *r.Set.DisplayMode
	}
}

func matchesDataset(patterns []string, dataset string) bool {
	for _, pattern := range patterns {
		if matched, _ := path.Match(pattern, dataset); matched {
			return true
		}
	}
	return false
}
