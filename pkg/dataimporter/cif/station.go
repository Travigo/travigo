package cif

import (
	"bufio"
	"io"
)

type PhysicalStation struct {
	StationName                string
	CATEInterchangeStatus      string
	TIPLOCCode                 string
	MinorCRSCode               string
	CRSCode                    string
	OrdnanceSurveyGridRefEast  string
	OrdnanceSurveyGridRefNorth string
	MinimumChangeTime          string
}

type StationAlias struct {
	StationName  string
	StationAlias string
}

func (c *CommonInterfaceFormat) ParseMSN(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		recordIdentity := line[0:1]

		switch recordIdentity {
		case "A":
			physicalStation := PhysicalStation{
				StationName:                line[1:5],
				CATEInterchangeStatus:      line[35:36],
				TIPLOCCode:                 line[36:43],
				MinorCRSCode:               line[43:43],
				CRSCode:                    line[49:52],
				OrdnanceSurveyGridRefEast:  line[52:57],
				OrdnanceSurveyGridRefNorth: line[58:63],
				MinimumChangeTime:          line[63:65],
			}
			c.PhysicalStations = append(c.PhysicalStations, physicalStation)
		case "L":
			stationAlias := StationAlias{
				StationName:  line[5:31],
				StationAlias: line[36:62],
			}
			c.StationAliases = append(c.StationAliases, stationAlias)
		}
	}
}
