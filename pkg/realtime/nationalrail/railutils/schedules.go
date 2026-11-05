package railutils

const (
	VSTPDataset            = "gb-networkrail-vstp"
	DarwinAmendmentDataset = "gb-nationalrail-darwin-amendment"
)

func DarwinAmendmentID(journeyID, serviceDate string) string {
	return "darwin-amendment:" + serviceDate + ":" + journeyID
}
