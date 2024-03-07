package travelinenoc

type NOCTableRecord struct {
	NOCCode            string `xml:"NOCCODE"`
	OperatorPublicName string
	VOSAPSVLicenseName string `xml:"VOSA_PSVLicenseName"`
	OperatorID         string `xml:"OpId"`
	PublicNameID       string `xml:"PubNmId"`
}
