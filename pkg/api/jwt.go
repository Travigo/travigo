package api

import (
	"context"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
	"github.com/gofiber/fiber/v2"
)

// CustomClaims contains custom data we want from the token.
type CustomClaims struct {
	Scope string `json:"scope"`
}

// Validate does nothing for this example, but we need
// it to satisfy validator.CustomClaims interface.
func (c CustomClaims) Validate(ctx context.Context) error {
	return nil
}

// EnsureValidToken is a middleware that will check the validity of our JWT.
func EnsureValidToken() fiber.Handler {
	issuerURL, err := url.Parse("https://" + os.Getenv("AUTH0_DOMAIN") + "/")
	if err != nil {
		log.Fatalf("Failed to parse the issuer url: %v", err)
	}

	provider := jwks.NewCachingProvider(issuerURL, 5*time.Minute)

	jwtValidator, err := validator.New(
		provider.KeyFunc,
		validator.RS256,
		issuerURL.String(),
		[]string{os.Getenv("AUTH0_AUDIENCE")},
		validator.WithCustomClaims(
			func() validator.CustomClaims {
				return &CustomClaims{}
			},
		),
		validator.WithAllowedClockSkew(time.Minute),
	)
	if err != nil {
		log.Fatalf("Failed to set up the jwt validator")
	}

	return func(c *fiber.Ctx) (err error) {
		authHeader := c.Get("Authorization")

		if authHeader == "" {
			c.SendStatus(fiber.StatusUnauthorized)
			return c.JSON(fiber.Map{
				"error": "Authorization header is required",
			})
		}

		jwtToken := authHeader[7:]
		claimsI, jwtErr := jwtValidator.ValidateToken(context.Background(), jwtToken)

		if jwtErr == nil {
			claims := claimsI.(*validator.ValidatedClaims)

			c.Locals("account_userid", claims.RegisteredClaims.Subject)

			return c.Next()
		} else {
			c.SendStatus(fiber.StatusUnauthorized)
			return c.JSON(fiber.Map{
				"error": "Invalid auth token",
			})
		}
	}
}
