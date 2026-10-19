package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/store"
	"github.com/alexindevs/jackpot-backend/internal/transport"
)

var now = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	mvp     []store.MVPEntry
	duos    []store.DuoEntry
	game    *store.GameSummary
	players map[int64]map[string]bool
	chat    []store.ChatMessage
	rooms   map[string]int64
	err     error

	gotRole  store.Role
	gotLimit int
	gotSince time.Time
	gotAfter int
	gotChatN int
}

func (f *fakeStore) TopPlayers(_ context.Context, role store.Role, limit int) ([]store.MVPEntry, error) {
	f.gotRole, f.gotLimit = role, limit
	return f.mvp, f.err
}
func (f *fakeStore) TopDuos(_ context.Context, limit int) ([]store.DuoEntry, error) {
	f.gotLimit = limit
	return f.duos, f.err
}
func (f *fakeStore) Game(_ context.Context, id int64) (*store.GameSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.game == nil || f.game.ID != id {
		return nil, store.ErrNotFound
	}
	return f.game, nil
}
func (f *fakeStore) WasPlayer(_ context.Context, id int64, p string) (bool, error) {
	return f.players[id][p], f.err
}
func (f *fakeStore) GameChat(_ context.Context, _ int64, since time.Time, after, limit int) ([]store.ChatMessage, error) {
	f.gotSince, f.gotAfter, f.gotChatN = since, after, limit
	return f.chat, f.err
}
func (f *fakeStore) RoomGame(_ context.Context, room string) (int64, error) {
	if id, ok := f.rooms[room]; ok {
		return id, f.err
	}
	return 0, store.ErrNotFound
}

type fakeRooms []room.Info

func (f fakeRooms) ListRooms() []room.Info { return f }

func newAPI(t *testing.T, mod func(*Config)) *httptest.Server {
	t.Helper()
	cfg := Config{
		Store:   &fakeStore{},
		Rooms:   fakeRooms{},
		Auth:    transport.Dev{},
		Origins: []string{"jackpot.start.ng", "localhost:*"},
		Now:     func() time.Time { return now },
	}
	if mod != nil {
		mod(&cfg)
	}
	srv := httptest.NewServer(Handler(cfg))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string, headers ...string) (*http.Response, any) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func errCode(body any) string {
	m, _ := body.(map[string]any)
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestRoomsListIsPublicAndNeverNull(t *testing.T) {
	srv := newAPI(t, nil)
	resp, body := get(t, srv, "/api/rooms")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if list, ok := body.([]any); !ok || len(list) != 0 {
		t.Errorf("an empty list should be [], got %v", body)
	}

	srv = newAPI(t, func(c *Config) {
		c.Rooms = fakeRooms{{ID: "abc123", Creator: "7", Phase: room.PhaseLobby, Players: 3, Teams: 1, MaxPlayers: 10, Joinable: true}}
	})
	_, body = get(t, srv, "/api/rooms")
	r := body.([]any)[0].(map[string]any)
	if r["id"] != "abc123" || r["phase"] != "lobby" || r["players"] != float64(3) || r["maxPlayers"] != float64(10) || r["joinable"] != true {
		t.Errorf("room = %v", r)
	}
}

func TestEverythingButRoomsNeedsTheDatabase(t *testing.T) {
	srv := newAPI(t, func(c *Config) { c.Store = nil })
	if resp, _ := get(t, srv, "/api/rooms"); resp.StatusCode != 200 {
		t.Errorf("rooms without a database: %d", resp.StatusCode)
	}
	for _, p := range []string{"/api/leaderboards/mvp?role=holder", "/api/leaderboards/duos", "/api/games/1", "/api/games/1/chat"} {
		resp, body := get(t, srv, p)
		if resp.StatusCode != 503 || errCode(body) != "database_unavailable" {
			t.Errorf("%s: %d %v", p, resp.StatusCode, body)
		}
	}
}

func TestMVPBoard(t *testing.T) {
	f := &fakeStore{mvp: []store.MVPEntry{
		{Rank: 1, Player: store.PlayerRef{ID: "7", Name: "Trini", Style: json.RawMessage(`{"hair":"braids"}`)}, Wins: 5},
		{Rank: 2, Player: store.PlayerRef{ID: "9", Name: "9"}, Wins: 2},
	}}
	srv := newAPI(t, func(c *Config) { c.Store = f })

	resp, body := get(t, srv, "/api/leaderboards/mvp?role=proclaimer&limit=2")
	if resp.StatusCode != 200 || f.gotRole != store.RoleProclaimer || f.gotLimit != 2 {
		t.Fatalf("status %d, role %q, limit %d", resp.StatusCode, f.gotRole, f.gotLimit)
	}
	first := body.([]any)[0].(map[string]any)
	player := first["player"].(map[string]any)
	if first["rank"] != float64(1) || first["wins"] != float64(5) || player["id"] != "7" || player["name"] != "Trini" {
		t.Errorf("entry = %v", first)
	}
	if player["style"].(map[string]any)["hair"] != "braids" {
		t.Errorf("a player's look should pass through: %v", player)
	}
	if resp.Header.Get("Cache-Control") != "public, max-age=10" {
		t.Errorf("cache = %q", resp.Header.Get("Cache-Control"))
	}

	get(t, srv, "/api/leaderboards/mvp?role=holder")
	if f.gotRole != store.RoleHolder || f.gotLimit != defaultLimit {
		t.Errorf("defaults: role %q, limit %d", f.gotRole, f.gotLimit)
	}
	get(t, srv, "/api/leaderboards/mvp?role=holder&limit=100000")
	if f.gotLimit != maxLimit {
		t.Errorf("limit should be capped at %d, got %d", maxLimit, f.gotLimit)
	}
}

func TestBadLeaderboardRequests(t *testing.T) {
	srv := newAPI(t, nil)
	for _, p := range []string{
		"/api/leaderboards/mvp", "/api/leaderboards/mvp?role=king", "/api/leaderboards/mvp?role=holder&limit=0",
		"/api/leaderboards/mvp?role=holder&limit=-3", "/api/leaderboards/mvp?role=holder&limit=abc", "/api/leaderboards/duos?limit=x",
		"/api/games/abc", "/api/games/0", "/api/games/-1",
	} {
		resp, body := get(t, srv, p)
		if resp.StatusCode != 400 || errCode(body) != "bad_request" {
			t.Errorf("%s: %d %v", p, resp.StatusCode, body)
		}
	}
}

func TestDuosBoard(t *testing.T) {
	f := &fakeStore{duos: []store.DuoEntry{{
		Rank: 1, Wins: 4, Games: 6,
		Players: [2]store.PlayerRef{{ID: "7", Name: "Trini"}, {ID: "9", Name: "Ozi"}},
	}}}
	srv := newAPI(t, func(c *Config) { c.Store = f })
	_, body := get(t, srv, "/api/leaderboards/duos?limit=5")
	d := body.([]any)[0].(map[string]any)
	pair := d["players"].([]any)
	if f.gotLimit != 5 || d["wins"] != float64(4) || d["games"] != float64(6) || len(pair) != 2 || pair[1].(map[string]any)["name"] != "Ozi" {
		t.Errorf("duo = %v (limit %d)", d, f.gotLimit)
	}
}

func TestEmptyBoardsAreEmptyLists(t *testing.T) {
	srv := newAPI(t, nil)
	for _, p := range []string{"/api/leaderboards/mvp?role=holder", "/api/leaderboards/duos"} {
		if _, body := get(t, srv, p); body == nil {
			t.Errorf("%s returned null", p)
		}
	}
}

func TestGameSummary(t *testing.T) {
	winner := 1
	reason := "wins"
	f := &fakeStore{game: &store.GameSummary{
		ID: 42, Room: "abc123", StartedAt: now, Status: "finished", EndReason: &reason, WinnerTeam: &winner, CountsForStats: true,
		Players: []store.GamePlayer{{Player: store.PlayerRef{ID: "7", Name: "Trini"}, Team: 1}},
		Rounds:  []store.RoundSummary{{Round: 1, Team: 1, Proclaimer: store.PlayerRef{ID: "7"}, Holder: store.PlayerRef{ID: "9"}, WonAt: now}},
	}}
	srv := newAPI(t, func(c *Config) { c.Store = f })
	resp, body := get(t, srv, "/api/games/42")
	g := body.(map[string]any)
	if resp.StatusCode != 200 || g["id"] != float64(42) || g["status"] != "finished" || g["winnerTeam"] != float64(1) || g["endReason"] != "wins" {
		t.Fatalf("game = %v", g)
	}
	if r := g["rounds"].([]any)[0].(map[string]any); r["proclaimer"].(map[string]any)["id"] != "7" || r["wonAt"] == nil {
		t.Errorf("round = %v", r)
	}
	if resp, body := get(t, srv, "/api/games/43"); resp.StatusCode != 404 || errCode(body) != "not_found" {
		t.Errorf("missing game: %d %v", resp.StatusCode, body)
	}
}

func TestChatNeedsASessionAndAGameYouPlayedIn(t *testing.T) {
	f := &fakeStore{
		players: map[int64]map[string]bool{42: {"alice": true}},
		chat:    []store.ChatMessage{{ID: 1, Player: store.PlayerRef{ID: "alice", Name: "alice"}, Text: "I said it first", At: now}},
	}
	srv := newAPI(t, func(c *Config) { c.Store = f })

	cases := []struct {
		name    string
		headers []string
		status  int
		code    string
	}{
		{"no token", nil, 401, "unauthorized"},
		{"not a bearer token", []string{"Authorization", "Basic abc"}, 401, "unauthorized"},
		{"bad session", []string{"Authorization", "Bearer nonsense"}, 401, "unauthorized"},
		{"signed in but not a player of that game", []string{"Authorization", "Bearer dev:mallory"}, 403, "not_a_player"},
		{"a player", []string{"Authorization", "Bearer dev:alice"}, 200, ""},
	}
	for _, c := range cases {
		resp, body := get(t, srv, "/api/games/42/chat", c.headers...)
		if resp.StatusCode != c.status || (c.code != "" && errCode(body) != c.code) {
			t.Errorf("%s: %d %v", c.name, resp.StatusCode, body)
		}
	}
	resp, body := get(t, srv, "/api/games/42/chat", "Authorization", "Bearer dev:alice")
	msgs := body.([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["text"] != "I said it first" || msgs[0].(map[string]any)["id"] != float64(1) {
		t.Errorf("messages = %v", body)
	}
	if !f.gotSince.IsZero() {
		t.Errorf("the API must not cut chat by age (the sweeper expires it a day after the game ends): since %v", f.gotSince)
	}
	if resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("chat must not be cached publicly: %q", resp.Header.Get("Cache-Control"))
	}
}

type outageAuth struct{}

func (outageAuth) Authenticate(context.Context, string) (transport.Identity, error) {
	return transport.Identity{}, errors.New("start.ng is down")
}

func TestAnAuthOutageIsNotMistakenForABadSession(t *testing.T) {
	srv := newAPI(t, func(c *Config) { c.Auth = outageAuth{}; c.Store = &fakeStore{} })
	resp, body := get(t, srv, "/api/games/1/chat", "Authorization", "Bearer whatever")
	if resp.StatusCode != 503 || errCode(body) != "auth_unavailable" {
		t.Errorf("%d %v", resp.StatusCode, body)
	}
}

func TestDatabaseErrorsAreHiddenFromClients(t *testing.T) {
	srv := newAPI(t, func(c *Config) {
		c.Store = &fakeStore{err: errors.New("password authentication failed for user jackpot")}
	})
	resp, body := get(t, srv, "/api/leaderboards/duos")
	if resp.StatusCode != 500 || errCode(body) != "internal" {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	if m := body.(map[string]any)["error"].(map[string]any)["message"].(string); m == "" || len(m) > 40 {
		t.Errorf("message = %q", m)
	}
}

func TestCORS(t *testing.T) {
	srv := newAPI(t, nil)
	check := func(origin string, wantAllowed bool) {
		t.Helper()
		resp, _ := get(t, srv, "/api/rooms", "Origin", origin)
		got := resp.Header.Get("Access-Control-Allow-Origin")
		if wantAllowed && got != origin || !wantAllowed && got != "" {
			t.Errorf("origin %q: Allow-Origin = %q", origin, got)
		}
		if resp.Header.Get("Vary") == "" {
			t.Error("responses must say they vary by Origin")
		}
	}
	check("https://jackpot.start.ng", true)
	check("http://localhost:5173", true)
	check("https://evil.example", false)
	check("https://jackpot.start.ng.evil.example", false)
	check("null", false)
	check("not a url", false)

	req, _ := http.NewRequest("OPTIONS", srv.URL+"/api/games/1/chat", nil)
	req.Header.Set("Origin", "https://jackpot.start.ng")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Access-Control-Allow-Headers") != "Authorization" || resp.Header.Get("Access-Control-Allow-Origin") != "https://jackpot.start.ng" {
		t.Errorf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestUnknownPathsAndMethods(t *testing.T) {
	srv := newAPI(t, nil)
	if resp, body := get(t, srv, "/api/nope"); resp.StatusCode != 404 || errCode(body) != "not_found" {
		t.Errorf("unknown path: %d %v", resp.StatusCode, body)
	}
	resp, err := http.Post(srv.URL+"/api/rooms", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/rooms = %d, want 405: the API is read-only", resp.StatusCode)
	}
}

func TestChatPollingByRoomAndMessageNumber(t *testing.T) {
	f := &fakeStore{
		players: map[int64]map[string]bool{42: {"alice": true}},
		rooms:   map[string]int64{"k3m9xq": 42},
		chat:    []store.ChatMessage{{ID: 8, Player: store.PlayerRef{ID: "alice", Name: "alice"}, Text: "latest", At: now}},
	}
	srv := newAPI(t, func(c *Config) { c.Store = f })
	auth := []string{"Authorization", "Bearer dev:alice"}

	resp, body := get(t, srv, "/api/rooms/k3m9xq/chat?after=7&limit=5", auth...)
	msgs, _ := body.([]any)
	if resp.StatusCode != 200 || len(msgs) != 1 || msgs[0].(map[string]any)["id"] != float64(8) {
		t.Fatalf("poll: %d %v", resp.StatusCode, body)
	}
	if f.gotAfter != 7 || f.gotChatN != 5 {
		t.Errorf("after %d, limit %d", f.gotAfter, f.gotChatN)
	}
	get(t, srv, "/api/rooms/k3m9xq/chat", auth...)
	if f.gotAfter != 0 || f.gotChatN != defaultChatLimit {
		t.Errorf("defaults: after %d, limit %d", f.gotAfter, f.gotChatN)
	}
	get(t, srv, "/api/rooms/k3m9xq/chat?limit=99999", auth...)
	if f.gotChatN != maxChatLimit {
		t.Errorf("limit should be capped at %d, got %d", maxChatLimit, f.gotChatN)
	}
	// The same parameters work on the game route.
	get(t, srv, "/api/games/42/chat?after=3", auth...)
	if f.gotAfter != 3 {
		t.Errorf("game route after = %d", f.gotAfter)
	}

	cases := []struct {
		name    string
		path    string
		headers []string
		status  int
		code    string
	}{
		{"no session", "/api/rooms/k3m9xq/chat", nil, 401, "unauthorized"},
		{"unknown room", "/api/rooms/zzzzzz/chat", auth, 404, "not_found"},
		{"not a player", "/api/rooms/k3m9xq/chat", []string{"Authorization", "Bearer dev:mallory"}, 403, "not_a_player"},
		{"bad after", "/api/rooms/k3m9xq/chat?after=-1", auth, 400, "bad_request"},
		{"bad after text", "/api/rooms/k3m9xq/chat?after=x", auth, 400, "bad_request"},
		{"bad limit", "/api/rooms/k3m9xq/chat?limit=0", auth, 400, "bad_request"},
	}
	for _, c := range cases {
		resp, body := get(t, srv, c.path, c.headers...)
		if resp.StatusCode != c.status || errCode(body) != c.code {
			t.Errorf("%s: %d %v", c.name, resp.StatusCode, body)
		}
	}
}

func TestAnUnknownRoomDoesNotLeakThroughAMissingSession(t *testing.T) {
	// Without a session you get 401 whether or not the room exists, so room ids can't be probed.
	srv := newAPI(t, func(c *Config) { c.Store = &fakeStore{rooms: map[string]int64{"real": 1}} })
	a, _ := get(t, srv, "/api/rooms/real/chat")
	b, _ := get(t, srv, "/api/rooms/fake/chat")
	if a.StatusCode != 401 || b.StatusCode != 401 {
		t.Errorf("statuses %d and %d, want 401 for both", a.StatusCode, b.StatusCode)
	}
}
