package transxchange

type StopPoint struct {
	AtcoCode string

	CommonName string `xml:"Descriptor>CommonName"`
}
