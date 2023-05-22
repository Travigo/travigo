package global

import (
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/source/databaselookup"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/dataaggregator/source/nationalrail"
	"github.com/travigo/travigo/pkg/dataaggregator/source/tfl"
	"github.com/travigo/travigo/pkg/util"
)

func GlobalSetup() {
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}

	tflAppKey := util.GetEnvironmentVariables()["TRAVIGO_TFL_API_KEY"]

	dataaggregator.GlobalAggregator.RegisterSource(tfl.Source{
		AppKey: tflAppKey,
	})
	dataaggregator.GlobalAggregator.RegisterSource(databaselookup.Source{})
	dataaggregator.GlobalAggregator.RegisterSource(localdepartureboard.Source{})
	dataaggregator.GlobalAggregator.RegisterSource(nationalrail.Source{})
}
