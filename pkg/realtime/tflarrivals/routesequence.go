package tflarrivals

type RouteSequenceResponse struct {
	LineStrings       []string           `json:"lineStrings"`
	OrderedLineRoutes []OrderedLineRoute `json:"orderedLineRoutes"`
}

type OrderedLineRoute struct {
	Name      string   `json:"name"`
	NaptanIDs []string `json:"naptanIds"`
}
