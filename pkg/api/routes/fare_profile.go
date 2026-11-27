package routes

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func getUserFareProfile(c *fiber.Ctx) error {
	userID := c.Locals("account_userid").(string)
	var profile ctdf.UserFareProfile
	err := database.GetCollection("user_fare_profiles").FindOne(c.UserContext(), bson.M{"userid": userID}).Decode(&profile)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return c.JSON(ctdf.UserFareProfile{DiscountCards: []ctdf.FareCardSelection{}})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if profile.DiscountCards == nil {
		profile.DiscountCards = []ctdf.FareCardSelection{}
	}
	return c.JSON(profile)
}

func putUserFareProfile(c *fiber.Ctx) error {
	var profile ctdf.UserFareProfile
	if err := c.BodyParser(&profile); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid fare profile."})
	}
	if err := profile.Validate(); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	userID := c.Locals("account_userid").(string)
	if strings.TrimSpace(userID) == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "No user account is available."})
	}
	for index := range profile.DiscountCards {
		profile.DiscountCards[index].DatasetID = strings.TrimSpace(profile.DiscountCards[index].DatasetID)
		profile.DiscountCards[index].SourceIdentifier = strings.TrimSpace(profile.DiscountCards[index].SourceIdentifier)
		profile.DiscountCards[index].Name = strings.TrimSpace(profile.DiscountCards[index].Name)
	}
	profile.UserID = userID
	profile.ModificationDateTime = time.Now().UTC()
	if profile.DiscountCards == nil {
		profile.DiscountCards = []ctdf.FareCardSelection{}
	}
	_, err := database.GetCollection("user_fare_profiles").ReplaceOne(
		c.UserContext(),
		bson.M{"userid": userID},
		profile,
		options.Replace().SetUpsert(true),
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(profile)
}

func listFareCardOptions(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 15*time.Second)
	defer cancel()

	versionCursor, err := database.GetCollection("fare_versions").Find(ctx, bson.M{})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	defer versionCursor.Close(ctx)
	activeVersions := make([]bson.M, 0)
	for versionCursor.Next(ctx) {
		var head ctdf.FareVersionHead
		if err := versionCursor.Decode(&head); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		if head.DatasetID != "" && head.ActiveVersionID != "" {
			activeVersions = append(activeVersions, bson.M{"datasetid": head.DatasetID, "fareversionid": head.ActiveVersionID})
		}
	}
	if err := versionCursor.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if len(activeVersions) == 0 {
		return c.JSON([]ctdf.FareCardOption{})
	}

	cursor, err := database.GetCollection("fare_rider_categories").Find(ctx, bson.M{"$or": activeVersions})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	defer cursor.Close(ctx)
	optionsByKey := make(map[string]ctdf.FareCardOption)
	now := time.Now().UTC()
	for cursor.Next(ctx) {
		var rider ctdf.FareRiderCategory
		if err := cursor.Decode(&rider); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		kind, profileOption := fareProfileOptionKind(rider)
		if !profileOption {
			continue
		}
		sourceID := strings.TrimSpace(rider.RailcardCode)
		if sourceID == "" {
			sourceID = strings.TrimSpace(rider.SourceIdentifier)
		}
		if sourceID == "" || rider.Name == "" {
			continue
		}
		if !fareCardRecordValidOn(rider, now) {
			continue
		}
		option := ctdf.FareCardOption{
			DatasetID:        rider.DatasetID,
			SourceIdentifier: sourceID,
			Code:             rider.RailcardCode,
			Name:             rider.Name,
			Provider:         fareCardProvider(rider),
			Kind:             kind,
			Type:             fareProfileOptionType(rider),
			EligibilityNotes: fareCardEligibilityNotes(rider),
			ProofRequired:    rider.ProofRequired,
			EligibilityURL:   rider.EligibilityURL,
			EffectiveFrom:    rider.EffectiveFrom,
			EffectiveTo:      rider.EffectiveTo,
		}
		key := rider.DatasetID + "\x00" + sourceID
		previous, exists := optionsByKey[key]
		if !exists || option.EffectiveFrom.After(previous.EffectiveFrom) {
			optionsByKey[key] = option
		}
	}
	if err := cursor.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	options := make([]ctdf.FareCardOption, 0, len(optionsByKey))
	for _, option := range optionsByKey {
		options = append(options, option)
	}
	sort.Slice(options, func(i, j int) bool {
		if !strings.EqualFold(options[i].Name, options[j].Name) {
			return strings.ToLower(options[i].Name) < strings.ToLower(options[j].Name)
		}
		if options[i].DatasetID != options[j].DatasetID {
			return options[i].DatasetID < options[j].DatasetID
		}
		return options[i].SourceIdentifier < options[j].SourceIdentifier
	})
	return c.JSON(options)
}

func fareProfileOptionKind(rider ctdf.FareRiderCategory) (string, bool) {
	userType := strings.ToLower(strings.TrimSpace(rider.UserType))
	if userType == "railcard" || userType == "discount_card" || rider.CardType != "" {
		return "discount_card", true
	}
	if rider.IsAgeOnlyCategory() || (rider.IsDefault && !rider.RequiresEligibilityProof()) {
		return "", false
	}
	if userType == "" && strings.TrimSpace(rider.Name) == "" {
		return "", false
	}
	return "fare_category", true
}

func fareProfileOptionType(rider ctdf.FareRiderCategory) string {
	if rider.CardType != "" {
		return rider.CardType
	}
	return rider.UserType
}

func fareCardRecordValidOn(rider ctdf.FareRiderCategory, date time.Time) bool {
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	if !rider.EffectiveFrom.IsZero() {
		from := time.Date(rider.EffectiveFrom.Year(), rider.EffectiveFrom.Month(), rider.EffectiveFrom.Day(), 0, 0, 0, 0, time.UTC)
		if date.Before(from) {
			return false
		}
	}
	if !rider.EffectiveTo.IsZero() {
		to := time.Date(rider.EffectiveTo.Year(), rider.EffectiveTo.Month(), rider.EffectiveTo.Day(), 0, 0, 0, 0, time.UTC)
		if date.After(to) {
			return false
		}
	}
	return true
}

func fareCardEligibilityNotes(rider ctdf.FareRiderCategory) []string {
	var notes []string
	if rider.MinimumAge != nil {
		notes = append(notes, "Minimum age "+strconv.Itoa(*rider.MinimumAge))
	}
	if rider.MaximumAge != nil {
		notes = append(notes, "Maximum age "+strconv.Itoa(*rider.MaximumAge))
	}
	if rider.HolderType == "A" {
		notes = append(notes, "Adult cardholder")
	} else if rider.HolderType == "C" {
		notes = append(notes, "Child cardholder")
	}
	if rider.MinimumPassengers > 1 {
		notes = append(notes, "Requires at least "+strconv.Itoa(rider.MinimumPassengers)+" passengers")
	}
	if rider.MinimumHolders > 1 {
		notes = append(notes, "Requires at least "+strconv.Itoa(rider.MinimumHolders)+" cardholders")
	}
	if rider.MinimumAdults > 0 {
		notes = append(notes, "Requires accompanying adult passengers")
	}
	if rider.MinimumChildren > 0 {
		notes = append(notes, "Requires accompanying child passengers")
	}
	if rider.RestrictedByArea {
		notes = append(notes, "Geographic restrictions apply")
	}
	if rider.RestrictedByTrain {
		notes = append(notes, "Train restrictions apply")
	}
	if rider.RestrictedByDate {
		notes = append(notes, "Date restrictions apply")
	}
	return notes
}

func fareCardProvider(rider ctdf.FareRiderCategory) string {
	if rider.DataSource != nil && rider.DataSource.ProviderName != "" {
		return rider.DataSource.ProviderName
	}
	return rider.DatasetID
}
