package transxchange

import "errors"

type RouteSection struct {
	ID string `xml:"id,attr"`

	RouteLinks []RouteLink `xml:"RouteLink"`
}

func (r *RouteSection) GetRouteLink(ID string) (*RouteLink, error) {
	for _, routeLink := range r.RouteLinks {
		if routeLink.ID == ID {
			return &routeLink, nil
		}
	}

	return nil, errors.New("could not find route link")
}

type RouteLink struct {
	ID                   string `xml:"id,attr"`
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	FromStop string `xml:"From>StopPointRef"`
	ToStop   string `xml:"To>StopPointRef"`
	Distance int

	Track []Location `xml:"Track>Mapping>Location"`
}
