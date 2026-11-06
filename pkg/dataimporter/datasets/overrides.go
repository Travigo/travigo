package datasets

import (
	"path"
	"slices"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Overrides struct {
	OperatorAliases []OperatorAlias
	ServiceBranding []ServiceBranding
}

type OperatorAlias struct {
	Datasets []string // Matches identifiers as written in this datasource.
	From     []string
	To       string
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
	Set            struct {
		Colour          *string
		SecondaryColour *string
		Icon            *string
		DisplayMode     *string
	}
}

func (r ServiceBranding) Apply(service *ctdf.Service) {
	if !slices.Contains(r.Operators, service.OperatorRef) ||
		(len(r.Services) > 0 && !slices.Contains(r.Services, service.ServiceName)) ||
		(len(r.TransportTypes) > 0 && !slices.Contains(r.TransportTypes, service.TransportType)) {
		return
	}

	if len(r.Datasets) > 0 && (service.DataSource == nil || !matchesDataset(r.Datasets, service.DataSource.DatasetID)) {
		return
	}

	if r.Set.Colour != nil {
		service.BrandColour = *r.Set.Colour
	}
	if r.Set.SecondaryColour != nil {
		service.SecondaryBrandColour = *r.Set.SecondaryColour
	}
	if r.Set.Icon != nil {
		service.BrandIcon = *r.Set.Icon
	}
	if r.Set.DisplayMode != nil {
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
