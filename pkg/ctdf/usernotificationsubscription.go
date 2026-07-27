package ctdf

import "time"

// UserNotificationSubscription is a stored notification definition. It is
// intentionally separate from UserEventSubscription until the notification
// delivery pipeline is migrated to use it.
type UserNotificationSubscription struct {
	PrimaryIdentifier string `bson:"primaryidentifier" json:"id"`
	UserID            string `bson:"userid" json:"-"`

	EventType EventType              `bson:"eventtype" json:"eventType"`
	Values    map[string]interface{} `bson:"values" json:"values"`

	CreationDateTime     time.Time `bson:"creationdatetime" json:"createdAt"`
	ModificationDateTime time.Time `bson:"modificationdatetime" json:"updatedAt"`
}
