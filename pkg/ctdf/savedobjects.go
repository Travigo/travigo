package ctdf

type SavedObject struct {
	PrimaryIdentifier string `groups:"basic"`
	UserID            string `groups:"basic"`

	Type             string `groups:"basic"`
	ObjectIdentifier string `groups:"basic"`
}
