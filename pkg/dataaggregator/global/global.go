package global

import (
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/source/databaselookup"
	"github.com/travigo/travigo/pkg/dataaggregator/source/datasources"
	"github.com/travigo/travigo/pkg/dataaggregator/source/journeyplanner"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/dataaggregator/source/tfl"
	"github.com/travigo/travigo/pkg/util"
)

func Setup() {
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}

	env := util.GetEnvironmentVariables()

	dataaggregator.GlobalAggregator.RegisterSource(tfl.Source{
		AppKey: env["TRAVIGO_TFL_API_KEY"],
	})

	databaseLookupSource := databaselookup.Source{}
	databaseLookupSource.Setup()
	dataaggregator.GlobalAggregator.RegisterSource(databaseLookupSource)

	localdepartureboardSource := localdepartureboard.Source{}
	localdepartureboardSource.Setup()
	dataaggregator.GlobalAggregator.RegisterSource(localdepartureboardSource)

	dataaggregator.GlobalAggregator.RegisterSource(journeyplanner.Source{})
	dataaggregator.GlobalAggregator.RegisterSource(datasources.Source{})
}
