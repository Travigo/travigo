package travelinenoc

type NOCLinesRecord struct {
	NOCCode       string `xml:"NOCCODE"`
	PublicName    string `xml:"PubNm"`
	ReferenceName string `xml:"RefNm"`
	Licence       string
	Mode          string
	EBSRAgent     string

	London          string `xml:"LO"`
	SouthWest       string `xml:"SW"`
	WestMidlands    string `xml:"WM"`
	Wales           string `xml:"WA"`
	Yorkshire       string `xml:"YO"`
	NorthWest       string `xml:"NW"`
	NorthEast       string `xml:"NE"`
	Scotland        string `xml:"SC"`
	SouthEast       string `xml:"SE"`
	EastAnglia      string `xml:"EA"`
	EastMidlands    string `xml:"EM"`
	NorthernIreland string `xml:"NI"`
}
