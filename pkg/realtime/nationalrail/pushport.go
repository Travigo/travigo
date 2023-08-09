package nationalrail

import "github.com/kr/pretty"

type PushPortData struct {
	TrainStatuses []TrainStatus
}

func (p *PushPortData) UpdateRealtimeJourneys() {
	for _, trainStatus := range p.TrainStatuses {
		pretty.Println(trainStatus.UID)
	}
}
