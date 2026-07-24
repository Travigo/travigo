package batchrunner

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/auth0/go-jwt-middleware/v2/jwks"
	"github.com/auth0/go-jwt-middleware/v2/validator"
	"github.com/travigo/travigo/pkg/api"
)

type tokenValidator func(context.Context, string) ([]string, error)

func newTokenValidator() (tokenValidator, error) {
	issuerURL, err := url.Parse("https://" + os.Getenv("AUTH0_DOMAIN") + "/")
	if err != nil {
		return nil, err
	}

	provider := jwks.NewCachingProvider(issuerURL, 5*time.Minute)
	jwtValidator, err := validator.New(
		provider.KeyFunc,
		validator.RS256,
		issuerURL.String(),
		[]string{os.Getenv("AUTH0_AUDIENCE")},
		validator.WithCustomClaims(func() validator.CustomClaims { return &api.CustomClaims{} }),
		validator.WithAllowedClockSkew(time.Minute),
	)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, token string) ([]string, error) {
		claimsI, err := jwtValidator.ValidateToken(ctx, token)
		if err != nil {
			return nil, err
		}
		return claimsI.(*validator.ValidatedClaims).CustomClaims.(*api.CustomClaims).Permissions, nil
	}, nil
}

func requireAdmin(next http.Handler, validate tokenValidator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") || len(authHeader) == len("Bearer ") {
			writeError(w, http.StatusUnauthorized, "Authorization header is required")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if internalToken := os.Getenv("TRAVIGO_BATCH_RUNNER_AUTH_TOKEN"); internalToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(internalToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		permissions, err := validate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Invalid auth token")
			return
		}
		if !slices.Contains(permissions, "admin:all") {
			writeError(w, http.StatusUnauthorized, "Admin permission is required")
			return
		}

		next.ServeHTTP(w, r)
	})
}
