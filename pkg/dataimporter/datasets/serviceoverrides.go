package datasets

import "github.com/travigo/travigo/pkg/ctdf"

// ServiceOverrides retains datasource and YAML order within each operator.
type ServiceOverrides struct {
	branding map[string][]ServiceBranding
	metadata map[string][]ServiceMetadata
}

func NewServiceOverrides(sources []DataSource) ServiceOverrides {
	rules := ServiceOverrides{
		branding: make(map[string][]ServiceBranding),
		metadata: make(map[string][]ServiceMetadata),
	}
	for _, source := range sources {
		for _, rule := range source.Overrides.ServiceBranding {
			for _, operator := range rule.Operators {
				rules.branding[operator] = append(rules.branding[operator], rule)
			}
		}
		for _, rule := range source.Overrides.ServiceMetadata {
			for _, operator := range rule.Operators {
				rules.metadata[operator] = append(rules.metadata[operator], rule)
			}
		}
	}
	return rules
}

// Apply enriches a fresh copy of a publisher service during publication.
func (rules ServiceOverrides) Apply(service *ctdf.Service) bool {
	selector := *service
	selector.ServiceName = service.LineName()
	if selector.DataSource != nil && selector.DataSource.DatasetID == "gb-dft-gb-dft-bods-gtfs-schedule-north-west" {
		// Keep the registered dataset and record IDs while matching the rule scope.
		source := *selector.DataSource
		source.DatasetID = "gb-dft-bods-gtfs-schedule-north-west"
		selector.DataSource = &source
	}

	matched := false
	for _, rule := range rules.branding[selector.OperatorRef] {
		if rule.Matches(&selector) {
			rule.apply(service)
			matched = true
		}
	}
	for _, rule := range rules.metadata[selector.OperatorRef] {
		if rule.Matches(&selector) {
			rule.apply(service)
			matched = true
		}
	}
	if service.ServiceName != selector.ServiceName {
		service.PublisherServiceName = &selector.ServiceName
	}
	return matched
}
