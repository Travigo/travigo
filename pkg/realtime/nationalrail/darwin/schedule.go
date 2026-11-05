package darwin

import "encoding/xml"

type Schedule struct {
	RID string `xml:"rid,attr"`
	UID string `xml:"uid,attr"`
	SSD string `xml:"ssd,attr"`
	TOC string `xml:"toc,attr"`

	CancelReason string `xml:"cancelReason"`

	// Darwin can send more than one origin or destination when a service is
	// partially cancelled. Locations must remain in document order so a live
	// origin/destination can supersede the cancelled location at the same stop.
	Locations []ScheduleStop
}

type ScheduleStop struct {
	Type     string
	Tiploc   string `xml:"tpl,attr"`
	Activity string `xml:"act,attr"`

	PublicDeparture  string `xml:"ptd,attr"`
	WorkingDeparture string `xml:"wtd,attr"`

	PublicArrival  string `xml:"pta,attr"`
	WorkingArrival string `xml:"wta,attr"`

	Platform string `xml:"plat,attr"`

	Cancelled string `xml:"can,attr"`
}

func (s *Schedule) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*s = Schedule{}

	for _, attribute := range start.Attr {
		switch attribute.Name.Local {
		case "rid":
			s.RID = attribute.Value
		case "uid":
			s.UID = attribute.Value
		case "ssd":
			s.SSD = attribute.Value
		case "toc":
			s.TOC = attribute.Value
		}
	}

	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}

		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == "cancelReason" {
				if err := decoder.DecodeElement(&s.CancelReason, &element); err != nil {
					return err
				}
				continue
			}

			if !isDarwinScheduleLocation(element.Name.Local) {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}

			var stop ScheduleStop
			if err := decoder.DecodeElement(&stop, &element); err != nil {
				return err
			}
			stop.Type = element.Name.Local
			s.Locations = append(s.Locations, stop)
		case xml.EndElement:
			if element.Name == start.Name {
				return nil
			}
		}
	}
}

func isDarwinScheduleLocation(elementName string) bool {
	switch elementName {
	case "OR", "OPOR", "IP", "OPIP", "PP", "DT", "OPDT":
		return true
	default:
		return false
	}
}

func (s ScheduleStop) isPassengerCall() bool {
	switch s.Type {
	case "OR", "IP", "DT":
		return true
	default:
		return false
	}
}

// A partial cancellation can end and restart at the same timed call. Darwin
// reports the cancelled and live versions separately, but predictions refer
// to the live call. Genuine repeat visits have separate schedule times.
func (s Schedule) passengerCalls() []ScheduleStop {
	calls := make([]ScheduleStop, 0, len(s.Locations))
	for _, call := range s.Locations {
		if !call.isPassengerCall() {
			continue
		}
		if len(calls) > 0 {
			previous := &calls[len(calls)-1]
			if sameDarwinBoundary(*previous, call) {
				if previous.Cancelled == "true" {
					*previous = call
				}
				continue
			}
		}
		calls = append(calls, call)
	}
	return calls
}

func sameDarwinBoundary(before, after ScheduleStop) bool {
	if before.Tiploc != after.Tiploc || (before.Cancelled == "true") == (after.Cancelled == "true") ||
		(after.Type != "OR" && before.Type != "DT") {
		return false
	}
	beforeTime := scheduleClock(before.PublicDeparture, before.WorkingDeparture)
	if beforeTime.IsZero() {
		beforeTime = scheduleClock(before.PublicArrival, before.WorkingArrival)
	}
	afterTime := scheduleClock(after.PublicArrival, after.WorkingArrival)
	if afterTime.IsZero() {
		afterTime = scheduleClock(after.PublicDeparture, after.WorkingDeparture)
	}
	return !beforeTime.IsZero() && beforeTime.Equal(afterTime)
}
