// Package e2e plays a whole game through the real stack: WebSocket clients, the
// room, the async writer, a real Postgres, and the REST API on top. It is
// skipped unless JACKPOT_TEST_DATABASE_URL is set.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/alexindevs/jackpot-backend/internal/api"
	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/store"
	"github.com/alexindevs/jackpot-backend/internal/transport"
)

type msg = map[string]any

type client struct {
	t    *testing.T
	ws   *websocket.Conn
	mu   sync.Mutex
	msgs []msg
}

func dial(t *testing.T, wsURL, name string) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, ws: ws}
	t.Cleanup(func() { _ = ws.CloseNow() })
	go func() {
		for {
			_, b, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			var m msg
			if json.Unmarshal(b, &m) == nil {
				c.mu.Lock()
				c.msgs = append(c.msgs, m)
				c.mu.Unlock()
			}
		}
	}()
	c.send(msg{"type": "auth", "token": "dev:" + name})
	c.wait("hello", func(m msg) bool { return m["t"] == "hello" })
	return c
}

func (c *client) send(v msg) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) wait(what string, match func(msg) bool) msg {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, m := range c.msgs {
			if match(m) {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	c.t.Fatalf("timed out waiting for %s", what)
	return nil
}

func (c *client) ok(v msg) msg {
	c.t.Helper()
	id := float64(time.Now().UnixNano())
	v["id"] = id
	c.send(v)
	r := c.wait("a reply to "+fmt.Sprint(v["type"]), func(m msg) bool { return m["id"] == id })
	if r["t"] == "error" {
		c.t.Fatalf("%v failed: %v", v["type"], r["d"])
	}
	return r
}

func (c *client) hand() (h [4]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.msgs {
		if m["t"] == "hand_update" {
			for i, v := range m["d"].(map[string]any)["hand"].([]any) {
				h[i] = int(v.(float64))
			}
		}
	}
	return h
}

func total(h [4]int) int { return h[0] + h[1] + h[2] + h[3] }

func newDB(t *testing.T) *store.Store {
	t.Helper()
	admin := os.Getenv("JACKPOT_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set JACKPOT_TEST_DATABASE_URL to run the end-to-end test")
	}
	ctx := context.Background()
	name := fmt.Sprintf("jackpot_e2e_%d_%d", time.Now().UnixNano(), rand.IntN(1e6))
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	db, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func getJSON(t *testing.T, base, path string, headers ...string) (int, any) {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
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
	return resp.StatusCode, body
}

func TestAGameIsPlayedRecordedAndShownOnTheLeaderboards(t *testing.T) {
	db := newDB(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	writer := store.NewWriter(db, log, 64)

	cfg := room.DefaultConfig()
	cfg.Engine.WinsToFinish = 1 // one round decides the game
	cfg.OnRecord = writer.Record

	auth := transport.Dev{}
	ctx, cancel := context.WithCancel(context.Background())
	m := transport.NewManager(ctx, transport.Config{
		Game: "jackpot", Room: cfg, Auth: auth,
		OnSignIn: func(id transport.Identity) {
			writer.Seen(store.Player{ID: string(id.ID), Name: id.Name, Style: id.Style})
		},
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler(api.Config{Store: db, Rooms: m, Auth: auth, Origins: []string{"jackpot.start.ng"}}))
	mux.Handle("/", m.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { m.CloseAll(); cancel(); srv.Close(); m.Wait() })
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// Four players, two teams, one room.
	names := []string{"alice", "bob", "carol", "dave"}
	players := map[string]*client{}
	for _, n := range names {
		players[n] = dial(t, wsURL, n)
	}
	created := players["alice"].ok(msg{"type": "create_room"})
	roomID := created["d"].(map[string]any)["room"].(string)
	for _, n := range names[1:] {
		players[n].ok(msg{"type": "join", "room": roomID})
	}
	players["alice"].ok(msg{"type": "invite_partner", "to": "bob"})
	players["bob"].ok(msg{"type": "accept_partner", "from": "alice"})
	players["carol"].ok(msg{"type": "invite_partner", "to": "dave"})
	players["dave"].ok(msg{"type": "accept_partner", "from": "carol"})

	// The public room list shows it while it's in the lobby.
	status, body := getJSON(t, srv.URL, "/api/rooms")
	list, _ := body.([]any)
	if status != 200 || len(list) != 1 || list[0].(map[string]any)["id"] != roomID || list[0].(map[string]any)["players"] != float64(4) {
		t.Fatalf("rooms = %d %v", status, body)
	}

	players["alice"].ok(msg{"type": "start_now"})
	for _, n := range names {
		players[n].ok(msg{"type": "ready", "ready": true})
	}
	for _, n := range names {
		players[n].wait("a hand", func(m msg) bool { return m["t"] == "hand_update" })
	}

	// Play real passes until bob holds four circles, then alice shouts Jackpot.
	for i := 0; ; i++ {
		if i > 300 {
			t.Fatal("bob never got four circles")
		}
		var holder string
		eventually(t, "exactly one holder", func() bool {
			n := 0
			for name, c := range players {
				if total(c.hand()) == 5 {
					holder, n = name, n+1
				}
			}
			return n == 1
		})
		if bob := players["bob"].hand(); bob[0] >= 4 && holder != "bob" {
			break
		}
		hand := players[holder].hand()
		card, best := 0, -1
		if holder == "bob" {
			best = 1 << 30
			for ty, c := range hand {
				if ty != 0 && c > 0 && c < best {
					card, best = ty, c
				}
			}
		} else if hand[0] == 0 {
			for ty, c := range hand {
				if c > best {
					card, best = ty, c
				}
			}
		}
		players[holder].ok(msg{"type": "pass", "card": card})
	}
	players["alice"].ok(msg{"type": "proclaim"})
	for _, n := range names {
		players[n].wait("game over", func(m msg) bool { return m["t"] == "game_event" && m["kind"] == "game_over" })
	}

	// Let the writer finish, then read it all back through the REST API.
	wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wcancel()
	if err := writer.Close(wctx); err != nil {
		t.Fatal(err)
	}

	status, body = getJSON(t, srv.URL, "/api/leaderboards/mvp?role=proclaimer")
	top := body.([]any)
	if status != 200 || len(top) != 1 {
		t.Fatalf("proclaimers = %d %v", status, body)
	}
	if p := top[0].(map[string]any); p["wins"] != float64(1) || p["player"].(map[string]any)["name"] != "alice" {
		t.Errorf("top proclaimer = %v", p)
	}
	_, body = getJSON(t, srv.URL, "/api/leaderboards/mvp?role=holder")
	if p := body.([]any)[0].(map[string]any); p["player"].(map[string]any)["id"] != "bob" {
		t.Errorf("top holder = %v", p)
	}
	_, body = getJSON(t, srv.URL, "/api/leaderboards/duos")
	duos := body.([]any)
	if len(duos) != 1 {
		t.Fatalf("duos = %v", body)
	}
	pair := duos[0].(map[string]any)["players"].([]any)
	if pair[0].(map[string]any)["name"] != "alice" || pair[1].(map[string]any)["name"] != "bob" || duos[0].(map[string]any)["wins"] != float64(1) {
		t.Errorf("duo = %v", duos[0])
	}

	// The game summary: straight from the database, and over the API.
	g, err := db.Game(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "finished" || len(g.Players) != 4 || len(g.Rounds) != 1 || g.Rounds[0].Proclaimer.ID != "alice" || g.Rounds[0].Holder.ID != "bob" {
		t.Errorf("game = %+v", g)
	}
	status, body = getJSON(t, srv.URL, "/api/games/1")
	if status != 200 || body.(map[string]any)["endReason"] != "wins" {
		t.Errorf("game over the API = %d %v", status, body)
	}
	// And a player can read the chat endpoint, an outsider can't.
	if status, _ := getJSON(t, srv.URL, "/api/games/1/chat", "Authorization", "Bearer dev:alice"); status != 200 {
		t.Errorf("a player reading chat: %d", status)
	}
	if status, _ := getJSON(t, srv.URL, "/api/games/1/chat", "Authorization", "Bearer dev:mallory"); status != 403 {
		t.Errorf("an outsider reading chat: %d", status)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
