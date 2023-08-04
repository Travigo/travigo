package realtime

import (
	"github.com/travigo/travigo/pkg/realtime/nationalrail"
	"github.com/travigo/travigo/pkg/realtime/tflarrivals"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "realtime",
		Usage: "Realtime sources",
		Subcommands: []*cli.Command{
			vehicletracker.RegisterCLI(),
			tflarrivals.RegisterCLI(),
			nationalrail.RegisterCLI(),
		},
	}
}
