package travelinenoc

type NOCTableRecord struct {
	NOCCode             string `xml:"NOCCODE"`
	OperatorPublicName  string
	VOSA_PSVLicenseName string
}
