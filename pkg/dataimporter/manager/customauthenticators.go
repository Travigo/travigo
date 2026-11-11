package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func customAuthNationalRailLogin(ctx context.Context, client *http.Client, endpoint, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", errors.New("TRAVIGO_NATIONALRAIL_USERNAME and TRAVIGO_NATIONALRAIL_PASSWORD must be set")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	form := url.Values{"username": {username}, "password": {password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", safeHTTPError("create National Rail authentication request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", safeHTTPError("authenticate with National Rail", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("National Rail authentication returned HTTP %d", resp.StatusCode)
	}

	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil || login.Token == "" {
		return "", errors.New("National Rail authentication did not return a valid token")
	}
	return login.Token, nil
}
