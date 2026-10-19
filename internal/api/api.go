// Package api serves the read-only REST API: the public room list,
// leaderboards, a finished game's summary, and a game's chat history.
// Gameplay itself runs over the WebSocket (see the transport package).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/store"
	"github.com/alexindevs/jackpot-backend/internal/transport"
	"github.com/alexindevs/jackpot-backend/internal/wirejson"
)

// Store is what the API needs from the database. *store.Store satisfies it.
type Store interface {
	TopPlayers(ctx context.Context, role store.Role, limit int) ([]store.MVPEntry, error)
	TopDuos(ctx context.Context, limit int) ([]store.DuoEntry, error)
	Game(ctx context.Context, id int64) (*store.GameSummary, error)
	WasPlayer(ctx context.Context, gameID int64, player string) (bool, error)
	GameChat(ctx context.Context, gameID int64, since time.Time, afterSeq, limit int) ([]store.ChatMessage, error)
	RoomGame(ctx context.Context, room string) (int64, error)
}

// Rooms lists the running rooms. *transport.Manager satisfies it.
type Rooms interface{ ListRooms() []room.Info }

// Config configures the API.
type Config struct {
	Store         Store // nil when no database is configured: those endpoints answer 503
	Rooms         Rooms
	Auth          transport.Authenticator
	Origins       []string      // hosts allowed to call the API from a browser, e.g. "jackpot.start.ng"
	ChatRetention time.Duration // how far back chat history goes (default 24h)
	Now           func() time.Time
}

const (
	defaultLimit     = 20
	maxLimit         = 100
	defaultChatLimit = 100
	maxChatLimit     = 200
	maxToken         = 4096
)

type handler struct{ cfg Config }

// Handler returns the API, to be mounted at /api/.
func Handler(cfg Config) http.Handler {
	if cfg.ChatRetention == 0 {
		cfg.ChatRetention = 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &handler{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/rooms", h.rooms)
	mux.HandleFunc("GET /api/leaderboards/mvp", h.mvp)
	mux.HandleFunc("GET /api/leaderboards/duos", h.duos)
	mux.HandleFunc("GET /api/games/{id}", h.game)
	mux.HandleFunc("GET /api/games/{id}/chat", h.gameChat)
	mux.HandleFunc("GET /api/rooms/{room}/chat", h.roomChat)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, OPTIONS")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "this API is read-only")
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return h.withCORS(mux)
}

// --- responses ---

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(wirejson.Convert(v))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, message
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, b)
}

func (h *handler) needStore(w http.ResponseWriter) bool {
	if h.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "the database is not configured")
		return false
	}
	return true
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if r.Context().Err() != nil {
		return // the client went away
	}
	writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
}

func limitParam(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	return min(n, maxLimit), true
}

// --- handlers ---

func (h *handler) rooms(w http.ResponseWriter, _ *http.Request) {
	infos := h.cfg.Rooms.ListRooms()
	if infos == nil {
		infos = []room.Info{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, infos)
}

func (h *handler) mvp(w http.ResponseWriter, r *http.Request) {
	if !h.needStore(w) {
		return
	}
	role := store.Role(r.URL.Query().Get("role"))
	if role != store.RoleProclaimer && role != store.RoleHolder {
		writeError(w, http.StatusBadRequest, "bad_request", "role must be proclaimer or holder")
		return
	}
	limit, ok := limitParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
		return
	}
	entries, err := h.cfg.Store.TopPlayers(r.Context(), role, limit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if entries == nil {
		entries = []store.MVPEntry{}
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, entries)
}

func (h *handler) duos(w http.ResponseWriter, r *http.Request) {
	if !h.needStore(w) {
		return
	}
	limit, ok := limitParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
		return
	}
	entries, err := h.cfg.Store.TopDuos(r.Context(), limit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if entries == nil {
		entries = []store.DuoEntry{}
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, entries)
}

func gameID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "game ids are positive numbers")
		return 0, false
	}
	return id, true
}

func (h *handler) game(w http.ResponseWriter, r *http.Request) {
	if !h.needStore(w) {
		return
	}
	id, ok := gameID(w, r)
	if !ok {
		return
	}
	g, err := h.cfg.Store.Game(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, g)
}

// signedIn checks the bearer token. It writes the error response itself and
// returns false if there is no valid session.
func (h *handler) signedIn(w http.ResponseWriter, r *http.Request) (transport.Identity, bool) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || token == "" || len(token) > maxToken {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in to read a game's chat")
		return transport.Identity{}, false
	}
	ident, err := h.cfg.Auth.Authenticate(r.Context(), token)
	switch {
	case errors.Is(err, transport.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "your session has expired; sign in again")
		return transport.Identity{}, false
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "sign-in is unavailable, try again")
		return transport.Identity{}, false
	}
	return ident, true
}

// gameChat returns a game's recent chat to someone who played in it. It
// needs a start.ng game session as a bearer token.
func (h *handler) gameChat(w http.ResponseWriter, r *http.Request) {
	if !h.needStore(w) {
		return
	}
	id, ok := gameID(w, r)
	if !ok {
		return
	}
	ident, ok := h.signedIn(w, r)
	if !ok {
		return
	}
	h.writeChat(w, r, id, ident)
}

// roomChat is the same, addressed by room id (which is what a client in a
// room knows) and answered for the room's most recent game. This is what a
// client polls, about once a minute, while its WebSocket is down.
func (h *handler) roomChat(w http.ResponseWriter, r *http.Request) {
	if !h.needStore(w) {
		return
	}
	ident, ok := h.signedIn(w, r)
	if !ok {
		return
	}
	id, err := h.cfg.Store.RoomGame(r.Context(), r.PathValue("room"))
	if err != nil {
		serverError(w, r, err)
		return
	}
	h.writeChat(w, r, id, ident)
}

// writeChat answers a chat request once we know the game and who is asking.
// ?after=N returns only messages numbered above N, and ?limit caps how many.
func (h *handler) writeChat(w http.ResponseWriter, r *http.Request, gameID int64, ident transport.Identity) {
	after, limit := 0, defaultChatLimit
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "after must be 0 or a message number")
			return
		}
		after = n
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive number")
			return
		}
		limit = min(n, maxChatLimit)
	}
	played, err := h.cfg.Store.WasPlayer(r.Context(), gameID, string(ident.ID))
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !played {
		writeError(w, http.StatusForbidden, "not_a_player", "only the players of a game can read its chat over the API")
		return
	}
	msgs, err := h.cfg.Store.GameChat(r.Context(), gameID, h.cfg.Now().Add(-h.cfg.ChatRetention), after, limit)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if msgs == nil {
		msgs = []store.ChatMessage{}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, msgs)
}

// --- CORS ---

// originAllowed reports whether a browser page on this origin may call the
// API. Patterns are hosts such as "jackpot.start.ng" or "localhost:*".
func (h *handler) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	for _, p := range h.cfg.Origins {
		if ok, _ := path.Match(p, u.Host); ok {
			return true
		}
	}
	return false
}

func (h *handler) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && h.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
