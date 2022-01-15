package transxchange

import (
	"errors"
)

type JourneyPatternSection struct {
	ID string `xml:"id,attr"`

	JourneyPatternTimingLinks []JourneyPatternTimingLink `xml:"JourneyPatternTimingLink"`
}

func (jp *JourneyPatternSection) GetTimingLink(ID string) (*JourneyPatternTimingLink, error) {
	for _, timingLink := range jp.JourneyPatternTimingLinks {
		if timingLink.ID == ID {
			return &timingLink, nil
		}
	}

	return nil, errors.New("Unable to find timing link")
}

type JourneyPatternTimingLink struct {
	ID string `xml:"id,attr"`

	RouteLinkRef string
	RunTime      string

	From JourneyPatternTimingLinkPoint
	To   JourneyPatternTimingLinkPoint
}

type JourneyPatternTimingLinkPoint struct {
	ID             string `xml:"id,attr"`
	SequenceNumber string `xml:",attr"`

	WaitTime                  string
	Activity                  string
	DynamicDestinationDisplay string
	StopPointRef              string
	TimingStatus              string
	FareStageNumber           string
}
