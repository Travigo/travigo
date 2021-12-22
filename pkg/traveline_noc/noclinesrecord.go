package travelinenoc

type NOCLinesRecord struct {
	NOCCode       string `xml:"NOCCODE"`
	PublicName    string `xml:"PubNm"`
	ReferenceName string `xml:"RefNm"`
	Licence       string
	Mode          string
	EBSRAgent     string
}
