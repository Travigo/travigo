package stats

import "github.com/travigo/travigo/pkg/ctdf"

type ArchivedJourney struct {
	ctdf.ArchivedJourney

	BundleSourceFile string

	Regions []string

	PrimaryOperatorRef string
	OperatorGroupRef   string
}
