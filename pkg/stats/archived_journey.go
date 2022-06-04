package stats

import "github.com/britbus/britbus/pkg/ctdf"

type ArchivedJourney struct {
	ctdf.ArchivedJourney

	BundleSourceFile string

	Regions []string

	PrimaryOperatorRef string
	OperatorGroupRef   string
}
