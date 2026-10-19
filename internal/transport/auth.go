package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Identity is who a connected player is. Name and Style come from start.ng
// and are never taken from the client.
type Identity struct {
	ID    engine.PlayerID `json:"id"`
	Name  string          `json:"name"`
	Style json.RawMessage `json:"style,omitempty"`
}

var ErrUnauthorized = errors.New("not signed in")

// Authenticator turns the token a client presents into an identity.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (Identity, error)
}

// StartNG checks a start.ng game session with start.ng's player endpoint:
// GET {BaseURL}/games/{Game}/player with the session as a bearer token.
//
// Whether start.ng allows this call from a server (the docs only describe
// browser use) has not been verified.
type StartNG struct {
	BaseURL string // e.g. https://api.start.ng
	Game    string
	Client  *http.Client
}

func (s StartNG) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" {
		return Identity{}, ErrUnauthorized
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	u := strings.TrimRight(s.BaseURL, "/") + "/games/" + url.PathEscape(s.Game) + "/player"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("start.ng unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Identity{}, ErrUnauthorized
	case resp.StatusCode != http.StatusOK:
		return Identity{}, fmt.Errorf("start.ng answered %d", resp.StatusCode)
	}
	var body struct {
		ID    int64           `json:"id"`
		Name  string          `json:"name"`
		Style json.RawMessage `json:"style"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return Identity{}, fmt.Errorf("unreadable start.ng reply: %w", err)
	}
	if body.ID == 0 {
		return Identity{}, ErrUnauthorized
	}
	return Identity{ID: engine.PlayerID(strconv.FormatInt(body.ID, 10)), Name: body.Name, Style: body.Style}, nil
}

// Dev accepts tokens of the form "dev:<name>" and makes <name> both the id
// and the display name. It exists for local testing and must never be wired
// up in production.
type Dev struct{}

func (Dev) Authenticate(_ context.Context, token string) (Identity, error) {
	name, found := strings.CutPrefix(token, "dev:")
	if !found || name == "" || len(name) > 40 {
		return Identity{}, ErrUnauthorized
	}
	return Identity{ID: engine.PlayerID(name), Name: name}, nil
}
