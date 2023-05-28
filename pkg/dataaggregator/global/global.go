package global

import (
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/source/databaselookup"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/dataaggregator/source/nationalrail"
	"github.com/travigo/travigo/pkg/dataaggregator/source/tfl"
	"github.com/travigo/travigo/pkg/util"
)

func Setup() {
	dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}

	env := util.GetEnvironmentVariables()

	dataaggregator.GlobalAggregator.RegisterSource(tfl.Source{
		AppKey: env["TRAVIGO_TFL_API_KEY"],
	})
	dataaggregator.GlobalAggregator.RegisterSource(databaselookup.Source{})
	dataaggregator.GlobalAggregator.RegisterSource(localdepartureboard.Source{})
	dataaggregator.GlobalAggregator.RegisterSource(nationalrail.Source{
		GatewayEndpoint: env["TRAVIGO_LDBWS_GATEWAY_ENDPOINT"],
	})
}
