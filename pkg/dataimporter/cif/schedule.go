package cif

import (
	"bufio"
	"io"
)

type TrainDefinitionSet struct {
	BasicSchedule             BasicSchedule
	BasicScheduleExtraDetails BasicScheduleExtraDetails
	OriginLocation            OriginLocation
	IntermediateLocations     []*IntermediateLocation
	ChangesEnRoute            []*ChangesEnRoute
	TerminatingLocation       TerminatingLocation
}

type BasicSchedule struct {
	TransactionType    string
	TrainUID           string
	DateRunsFrom       string
	DateRunsTo         string
	DaysRun            string
	BankHolidayRunning string
	TrainStatus        string
	TrainCategory      string
	TrainIdentity      string
	Headcode           string
	// CourseIndicator    string
	TrainServiceCode string
	// PortionID          string
	// PowerType          string
	// TimingLoad               string
	// Speed                    string
	// OperatingCharacteristics string
	// SeatingClass             string
	// Sleepers                 string
	// Reservations        string
	ConnectionIndicator string
	// CateringCode             string
	// ServiceBranding string
	// Spare           string
	STPIndicator string
}

type BasicScheduleExtraDetails struct {
	// TractionClass           string
	// UICCode                 string
	ATOCCode string
	// ApplicableTimetableCode string
}

type OriginLocation struct {
	Location               string
	ScheduledDepartureTime string
	PublicDepartureTime    string
	Platform               string
	// Line                   string
	// EngineeringAllowance   string
	// PathingAllowance       string
	Activity string
	// PerformanceAllowance   string
}

type IntermediateLocation struct {
	Location               string
	ScheduledArrivalTime   string
	ScheduledDepartureTime string
	// ScheduledPass          string
	PublicArrivalTime   string
	PublicDepartureTime string
	Platform            string
	// Line                string
	// Path                string
	Activity string
	// EngineeringAllowance string
	// PathingAllowance     string
	// PerformanceAllowance string
}

type ChangesEnRoute struct {
	Location      string
	TrainCategory string
	TrainIdentity string
	Headcode      string
	// CourseIndicator string
	// ProfitCentreCode string
	// BusinessSector   string
	// PowerType        string
	// TimingLoad       string
	// Speed string
	// OperatingChars   string
	// TrainClass       string
	// Sleepers         string
	// Reservations     string
	ConnectIndicator string
	// CateringCode     string
	// ServiceBranding  string
	// TractionClass    string
	// UICCode          string
	// RetailServiceID  string
}

type TerminatingLocation struct {
	Location             string
	ScheduledArrivalTime string
	PublicArrivalTime    string
	Platform             string
	Path                 string
	Activity             string
}

type BasicLocation struct {
	Location string

	ScheduledArrivalTime string
	PublicArrivalTime    string

	ScheduledDepartureTime string
	PublicDepartureTime    string

	Platform string

	Activity string
}

func (c *CommonInterfaceFormat) ParseMCA(reader io.Reader) {
	holdingTrainDef := false
	var currentTrainDef *TrainDefinitionSet

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		recordIdentity := line[0:2]

		switch recordIdentity {
		// case "AA":
		// 	association := Association{
		// 		TransactionType:     line[2:3],
		// 		BaseUID:             line[3:9],
		// 		AssocUID:            line[9:15],
		// 		AssocStartDate:      line[15:21],
		// 		AssocEndDate:        line[21:27],
		// 		AssocDays:           line[27:34],
		// 		AssocCat:            line[34:36],
		// 		AssocDateInd:        line[36:37],
		// 		AssocLocation:       line[37:44],
		// 		BaseLocationSuffix:  line[44:45],
		// 		AssocLocationSuffix: line[45:46],
		// 		DiagramType:         line[46:47],
		// 		AssociationType:     line[47:48],
		// 		STPIndicator:        line[79:80],
		// 	}

		// 	c.Associations = append(c.Associations, association)
		case "BS":
			if holdingTrainDef {
				c.TrainDefinitionSets = append(c.TrainDefinitionSets, currentTrainDef)
			}

			currentTrainDef = &TrainDefinitionSet{}

			currentTrainDef.BasicSchedule = BasicSchedule{
				TransactionType:    line[2:3],
				TrainUID:           line[3:9],
				DateRunsFrom:       line[9:15],
				DateRunsTo:         line[15:21],
				DaysRun:            line[21:28],
				BankHolidayRunning: line[28:29],
				TrainStatus:        line[29:30],
				TrainCategory:      line[30:32],
				TrainIdentity:      line[32:36],
				Headcode:           line[36:40],
				// CourseIndicator:    line[40:41],
				TrainServiceCode: line[41:49],
				// PortionID:          line[49:50],
				// PowerType:          line[50:53],
				// TimingLoad:               line[53:57],
				// Speed:                    line[57:60],
				// OperatingCharacteristics: line[60:66],
				// SeatingClass:             line[66:67],
				// Sleepers:                 line[67:68],
				// Reservations:        line[68:69],
				ConnectionIndicator: line[69:70],
				// CateringCode:             line[70:74],
				// ServiceBranding:          line[74:78],
				// Spare:                    line[78:79],
				STPIndicator: line[79:80],
			}

			holdingTrainDef = true
		case "BX":
			currentTrainDef.BasicScheduleExtraDetails = BasicScheduleExtraDetails{
				// TractionClass:           line[2:6],
				// UICCode:                 line[6:11],
				ATOCCode: line[11:13],
				// ApplicableTimetableCode: line[13:14],
			}
		case "LO":
			currentTrainDef.OriginLocation = OriginLocation{
				Location:               line[2:10],
				ScheduledDepartureTime: line[10:15],
				PublicDepartureTime:    line[15:19],
				Platform:               line[19:22],
				// Line:                   line[22:25],
				// EngineeringAllowance:   line[25:27],
				// PathingAllowance:       line[27:29],
				Activity: line[29:41],
				// PerformanceAllowance:   line[41:43],
			}
		case "LI":
			intermediateLocation := &IntermediateLocation{
				Location:               line[2:10],
				ScheduledArrivalTime:   line[10:15],
				ScheduledDepartureTime: line[15:20],
				// ScheduledPass:          line[20:25],
				PublicArrivalTime:   line[25:29],
				PublicDepartureTime: line[29:33],
				Platform:            line[33:36],
				// Line:                   line[36:39],
				// Path:                   line[39:42],
				Activity: line[42:54],
				// EngineeringAllowance:   line[54:56],
				// PathingAllowance:       line[56:58],
				// PerformanceAllowance:   line[58:60],
			}

			currentTrainDef.IntermediateLocations = append(currentTrainDef.IntermediateLocations, intermediateLocation)
		case "CR":
			changesEnRoute := &ChangesEnRoute{
				Location:      line[2:10],
				TrainCategory: line[10:12],
				TrainIdentity: line[12:16],
				Headcode:      line[16:20],
				// CourseIndicator:  line[20:28],
				// ProfitCentreCode: line[28:29],
				// BusinessSector:   line[29:30],
				// PowerType:        line[30:33],
				// TimingLoad:       line[33:37],
				// Speed:            line[37:40],
				// OperatingChars:   line[40:46],
				// TrainClass:       line[46:47],
				// Sleepers:         line[47:48],
				// Reservations:     line[48:49],
				ConnectIndicator: line[49:50],
				// CateringCode:     line[50:54],
				// ServiceBranding:  line[54:58],
				// TractionClass:    line[58:62],
				// UICCode:          line[62:67],
				// RetailServiceID:  line[67:75],
			}
			currentTrainDef.ChangesEnRoute = append(currentTrainDef.ChangesEnRoute, changesEnRoute)
		case "LT":
			currentTrainDef.TerminatingLocation = TerminatingLocation{
				Location:             line[2:10],
				ScheduledArrivalTime: line[10:15],
				PublicArrivalTime:    line[15:19],
				Platform:             line[19:22],
				Path:                 line[22:25],
				Activity:             line[25:37],
			}

			c.TrainDefinitionSets = append(c.TrainDefinitionSets, currentTrainDef)
			holdingTrainDef = false
		}
	}
}
