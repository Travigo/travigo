package ctdf

type SavedObject struct {
	PrimaryIdentifier string `groups:"basic,web-saved"`
	UserID            string `groups:"basic" json:"-"`

	Type             string      `groups:"basic,web-saved"`
	ObjectIdentifier string      `groups:"basic,web-saved"`
	Object           interface{} `groups:"web-saved" bson:"-" json:",omitempty"`
}
