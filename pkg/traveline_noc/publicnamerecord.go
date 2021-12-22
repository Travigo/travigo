package travelinenoc

type PublicNameRecord struct {
	PublicNameID       string `xml:"PubNmId"`
	OperatorPublicName string
	PublicNameQual     string `xml:"PubNmQual"`
	TTRteEnq           string
	FareEnq            string
	LostPropEnq        string
	DisruptEnq         string
	ComplEnq           string
	Twitter            string
	Facebook           string
	LinkedIn           string
	YouTube            string
	Website            string
}
