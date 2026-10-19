package engine

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
)

// PlayerSpec describes one player joining a game.
type PlayerSpec struct {
	ID   PlayerID
	Team TeamID
}

type player struct {
	id          PlayerID
	team        TeamID
	active      bool
	hand        Hand
	points      int
	canProclaim bool // revoked by a false proclamation made at 0 points
}

type pendingReveal struct {
	required int
	options  []CardType // types held at the moment of suspicion
}

// State is one game. It is not safe for concurrent use: the room runner owns
// it and calls Apply from a single goroutine.
type State struct {
	cfg Config
	rng *rand.Rand

	phase   Phase
	round   int
	players map[PlayerID]*player
	seats   []PlayerID // ring order, active players only
	teams   []TeamID   // active teams, ascending
	wins    map[TeamID]int
	pool    Hand
	holder  PlayerID

	pendingReveals map[PlayerID]*pendingReveal
	forcedPass     PlayerID // a caught holder who must pass before resetting
	votes          map[PlayerID]bool
	reshuffle      bool // reseat before the next round
	result         *Result
}

// New creates a game. There must be 2 to 5 teams of exactly two players, and
// the deck must be large enough for everyone to start with one of each type
// plus the extra passing card.
func New(cfg Config, specs []PlayerSpec, rng *rand.Rand) (*State, error) {
	n := len(specs)
	if n < 4 || n > 10 || n%2 != 0 {
		return nil, fmt.Errorf("need 4 to 10 players in pairs, got %d", n)
	}
	if cfg.CopiesPerType < n+1 {
		return nil, fmt.Errorf("need at least %d copies per type for %d players, got %d", n+1, n, cfg.CopiesPerType)
	}
	if cfg.SuspicionPoints < 0 || cfg.WinsToFinish < 1 || cfg.WinsToFinish > len(Letters) {
		return nil, errors.New("invalid config")
	}
	s := &State{
		cfg:            cfg,
		rng:            rng,
		players:        make(map[PlayerID]*player, n),
		wins:           map[TeamID]int{},
		pendingReveals: map[PlayerID]*pendingReveal{},
		votes:          map[PlayerID]bool{},
	}
	perTeam := map[TeamID]int{}
	var ids []PlayerID
	for _, sp := range specs {
		if sp.ID == "" {
			return nil, errors.New("empty player id")
		}
		if _, dup := s.players[sp.ID]; dup {
			return nil, fmt.Errorf("duplicate player %q", sp.ID)
		}
		s.players[sp.ID] = &player{id: sp.ID, team: sp.Team, active: true, canProclaim: true}
		perTeam[sp.Team]++
		ids = append(ids, sp.ID)
	}
	for t, c := range perTeam {
		if c != 2 {
			return nil, fmt.Errorf("team %d has %d players, want 2", t, c)
		}
		s.teams = append(s.teams, t)
	}
	slices.Sort(s.teams)
	for t := range s.pool {
		s.pool[t] = cfg.CopiesPerType
	}
	s.seats = seatPlayers(rng, ids, s.teamOf)
	return s, nil
}

// Read-only accessors, for the room runner and tests.

func (s *State) Phase() Phase           { return s.phase }
func (s *State) Round() int             { return s.round }
func (s *State) Holder() PlayerID       { return s.holder }
func (s *State) Pool() Hand             { return s.pool }
func (s *State) Seats() []PlayerID      { return slices.Clone(s.seats) }
func (s *State) Teams() []TeamID        { return slices.Clone(s.teams) }
func (s *State) Wins(t TeamID) int      { return s.wins[t] }
func (s *State) Result() *Result        { return s.result }
func (s *State) Hand(id PlayerID) Hand  { return s.players[id].hand }
func (s *State) Points(id PlayerID) int { return s.players[id].points }

// Apply runs one command. On error the state is unchanged.
func (s *State) Apply(cmd Command) ([]Event, error) {
	if s.phase == PhaseOver {
		return nil, ErrGameOver
	}
	switch c := cmd.(type) {
	case StartRound:
		return s.startRound()
	case Pass:
		return s.pass(c.Player, c.Card, false)
	case AutoPass:
		return s.autoPass(c.Player)
	case Suspect:
		return s.suspect(c.By, c.Target)
	case Reveal:
		return s.reveal(c.Player, c.Types)
	case AutoReveal:
		return s.autoReveal(c.Player)
	case Proclaim:
		return s.proclaim(c.By)
	case RemoveTeam:
		return s.removeTeam(c.Team)
	case VoteEnd:
		return s.voteEnd(c.Player, c.Agree)
	}
	return nil, ErrUnknownCommand
}

// --- helpers ---

func (s *State) teamOf(id PlayerID) TeamID { return s.players[id].team }

func (s *State) activePlayer(id PlayerID) (*player, error) {
	p, ok := s.players[id]
	if !ok || !p.active {
		return nil, ErrUnknownPlayer
	}
	return p, nil
}

func (s *State) partnerOf(id PlayerID) PlayerID {
	t := s.players[id].team
	for _, p := range s.players {
		if p.team == t && p.id != id {
			return p.id
		}
	}
	return ""
}

// nextAfter is the next seat clockwise from id, skipping any player in skip.
func (s *State) nextAfter(id PlayerID, skip TeamID, useSkip bool) PlayerID {
	i := slices.Index(s.seats, id)
	for k := 1; k <= len(s.seats); k++ {
		cand := s.seats[(i+k)%len(s.seats)]
		if useSkip && s.players[cand].team == skip {
			continue
		}
		return cand
	}
	return ""
}

// oddOneOut picks the type the hand holds the fewest of (ties at random).
func (s *State) oddOneOut(h Hand) CardType {
	best := math.MaxInt
	var cands []CardType
	for t, c := range h {
		if c == 0 {
			continue
		}
		switch {
		case c < best:
			best, cands = c, []CardType{CardType(t)}
		case c == best:
			cands = append(cands, CardType(t))
		}
	}
	return cands[s.rng.IntN(len(cands))]
}

func (s *State) transfer(from, to PlayerID, card CardType, auto bool) Event {
	s.players[from].hand[card]--
	s.players[to].hand[card]++
	s.holder = to
	return Passed{From: from, To: to, Card: card, Auto: auto}
}

// --- rounds ---

func (s *State) startRound() ([]Event, error) {
	if s.phase != PhaseWaiting {
		return nil, ErrWrongPhase
	}
	var evs []Event
	if s.reshuffle {
		var ids []PlayerID
		for _, id := range s.seats {
			ids = append(ids, id)
		}
		s.seats = seatPlayers(s.rng, ids, s.teamOf)
		s.reshuffle = false
		evs = append(evs, SeatsReshuffled{Seats: slices.Clone(s.seats)})
	}
	s.round++
	for t := range s.pool {
		s.pool[t] = s.cfg.CopiesPerType
	}
	for _, id := range s.seats {
		p := s.players[id]
		p.hand = Hand{}
		for t := range p.hand {
			p.hand[t] = 1
			s.pool[t]--
		}
		p.points = s.cfg.SuspicionPoints
	}
	holder := s.seats[s.rng.IntN(len(s.seats))]
	extra := CardType(s.rng.IntN(NumTypes))
	s.players[holder].hand[extra]++
	s.pool[extra]--
	s.holder = holder
	s.pendingReveals = map[PlayerID]*pendingReveal{}
	s.forcedPass = ""
	s.phase = PhasePlaying
	return append(evs, RoundStarted{Round: s.round, Holder: holder}), nil
}

// --- passing ---

func (s *State) pass(from PlayerID, card CardType, auto bool) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	if _, err := s.activePlayer(from); err != nil {
		return nil, err
	}
	if from != s.holder {
		return nil, ErrNotHolder
	}
	if card >= NumTypes {
		return nil, ErrBadCard
	}
	if s.players[from].hand[card] == 0 {
		return nil, ErrNoSuchCard
	}
	evs := []Event{s.transfer(from, s.nextAfter(from, 0, false), card, auto)}
	if s.forcedPass == from {
		s.forcedPass = ""
		evs = append(evs, s.reset(from))
	}
	return evs, nil
}

func (s *State) autoPass(id PlayerID) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	if _, err := s.activePlayer(id); err != nil {
		return nil, err
	}
	if id != s.holder {
		return nil, ErrNotHolder
	}
	return s.pass(id, s.oddOneOut(s.players[id].hand), true)
}

// --- suspicion ---

func (s *State) suspect(by, target PlayerID) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	b, err := s.activePlayer(by)
	if err != nil {
		return nil, err
	}
	t, err := s.activePlayer(target)
	if err != nil {
		return nil, err
	}
	if by == target {
		return nil, ErrSelfSuspect
	}
	if b.points == 0 {
		return nil, ErrNoPoints
	}
	if s.forcedPass == target || s.pendingReveals[target] != nil {
		return nil, ErrBusy
	}

	b.points--
	// Judged on the hand as of this command's place in the event log: a
	// holder who already passed shows 2, one who has not shows 3.
	required := 2
	if t.hand.Total() >= 5 {
		required = 3
	}
	options := t.hand.Types()
	evs := []Event{SuspicionRaised{By: by, Target: target, PointsLeft: b.points, Required: required}}
	if len(options) >= required {
		s.pendingReveals[target] = &pendingReveal{required: required, options: options}
		return append(evs, RevealRequested{Target: target, Required: required, Options: options}), nil
	}
	evs = append(evs, SuspicionFailed{Target: target, Hand: t.hand})
	if target == s.holder {
		s.forcedPass = target
		return append(evs, ForcedPassRequested{Player: target}), nil
	}
	return append(evs, s.reset(target)), nil
}

func (s *State) reveal(id PlayerID, types []CardType) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	pr := s.pendingReveals[id]
	if pr == nil {
		return nil, ErrNoPending
	}
	if len(types) != pr.required {
		return nil, ErrBadReveal
	}
	seen := map[CardType]bool{}
	for _, t := range types {
		if seen[t] || !slices.Contains(pr.options, t) {
			return nil, ErrBadReveal
		}
		seen[t] = true
	}
	delete(s.pendingReveals, id)
	shown := slices.Clone(types)
	slices.Sort(shown)
	return []Event{SuspicionFreed{Target: id, Revealed: shown}}, nil
}

func (s *State) autoReveal(id PlayerID) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	pr := s.pendingReveals[id]
	if pr == nil {
		return nil, ErrNoPending
	}
	opts := slices.Clone(pr.options)
	s.rng.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	return s.reveal(id, opts[:pr.required])
}

// reset returns a caught player's cards to the pool and deals them a fresh
// hand: one of each type. If the pool is short of a type, the slot is filled
// with whatever the pool has, so the hand is always four cards.
func (s *State) reset(id PlayerID) Event {
	p := s.players[id]
	for t := range p.hand {
		s.pool[t] += p.hand[t]
		p.hand[t] = 0
	}
	var short []CardType
	for t := range p.hand {
		if s.pool[t] > 0 {
			s.pool[t]--
			p.hand[t]++
		} else {
			short = append(short, CardType(t))
		}
	}
	for p.hand.Total() < NumTypes {
		var avail []int
		for t, c := range s.pool {
			if c > 0 {
				avail = append(avail, t)
			}
		}
		if len(avail) == 0 {
			break
		}
		t := avail[s.rng.IntN(len(avail))]
		s.pool[t]--
		p.hand[t]++
	}
	return PlayerReset{Player: id, ShortOf: short}
}

// --- proclaiming ---

func (s *State) proclaim(by PlayerID) ([]Event, error) {
	if s.phase != PhasePlaying {
		return nil, ErrWrongPhase
	}
	p, err := s.activePlayer(by)
	if err != nil {
		return nil, err
	}
	if !p.canProclaim {
		return nil, ErrProclaimRevoked
	}
	partnerID := s.partnerOf(by)
	if partnerID == s.holder {
		return nil, ErrPartnerHoldsPassingCard
	}
	if s.players[partnerID].hand.HasFourOfAKind() {
		s.wins[p.team]++
		w := s.wins[p.team]
		evs := []Event{RoundWon{Team: p.team, Proclaimer: by, Holder: partnerID, Wins: w, Letter: string(Letters[w-1])}}
		if w >= s.cfg.WinsToFinish {
			return append(evs, s.finish(Result{Winner: p.team, Reason: EndWins, CountsForStats: true})), nil
		}
		s.phase = PhaseWaiting
		s.holder = ""
		s.pendingReveals = map[PlayerID]*pendingReveal{}
		s.forcedPass = ""
		return evs, nil
	}
	if p.points == 0 {
		p.canProclaim = false
		return []Event{FalseProclaim{By: by, PointsLeft: 0, Revoked: true}}, nil
	}
	p.points--
	return []Event{FalseProclaim{By: by, PointsLeft: p.points}}, nil
}

// --- leaving ---

func (s *State) removeTeam(team TeamID) ([]Event, error) {
	if !slices.Contains(s.teams, team) {
		return nil, ErrUnknownTeam
	}
	var members []PlayerID
	for _, id := range s.seats {
		if s.players[id].team == team {
			members = append(members, id)
		}
	}
	var evs []Event
	if s.phase == PhasePlaying && s.players[s.holder].team == team {
		// The passing card goes on to the next player who is staying.
		to := s.nextAfter(s.holder, team, true)
		evs = append(evs, s.transfer(s.holder, to, s.oddOneOut(s.players[s.holder].hand), true))
	}
	for _, id := range members {
		p := s.players[id]
		for t := range p.hand {
			s.pool[t] += p.hand[t]
			p.hand[t] = 0
		}
		p.active = false
		delete(s.pendingReveals, id)
		delete(s.votes, id)
		if s.forcedPass == id {
			s.forcedPass = ""
		}
	}
	s.seats = slices.DeleteFunc(s.seats, func(id PlayerID) bool { return slices.Contains(members, id) })
	s.teams = slices.DeleteFunc(s.teams, func(t TeamID) bool { return t == team })
	evs = append(evs, TeamRemoved{Team: team, Members: members})

	if len(s.teams) < 2 {
		return append(evs, s.finish(Result{Winner: s.teams[0], Reason: EndForfeit, CountsForStats: false})), nil
	}
	if partnersAdjacent(s.seats, s.teamOf) {
		s.reshuffle = true
	}
	return append(evs, s.checkVotes()...), nil
}

// --- ending early ---

func (s *State) voteEnd(id PlayerID, agree bool) ([]Event, error) {
	if _, err := s.activePlayer(id); err != nil {
		return nil, err
	}
	if agree {
		s.votes[id] = true
	} else {
		delete(s.votes, id)
	}
	return append([]Event{VoteChanged{Player: id, Agree: agree}}, s.checkVotes()...), nil
}

// checkVotes ends the game if more than half the teams have both partners
// agreeing.
func (s *State) checkVotes() []Event {
	agreeing := 0
	for _, t := range s.teams {
		all := true
		for _, id := range s.seats {
			if s.players[id].team == t && !s.votes[id] {
				all = false
			}
		}
		if all {
			agreeing++
		}
	}
	if agreeing*2 <= len(s.teams) {
		return nil
	}
	top, leaders := -1, []TeamID(nil)
	for _, t := range s.teams {
		switch w := s.wins[t]; {
		case w > top:
			top, leaders = w, []TeamID{t}
		case w == top:
			leaders = append(leaders, t)
		}
	}
	r := Result{Reason: EndAgreement, CountsForStats: true}
	if len(leaders) == 1 {
		r.Winner = leaders[0]
	} else {
		r.Draw = true
	}
	return []Event{s.finish(r)}
}

func (s *State) finish(r Result) Event {
	r.Wins = map[TeamID]int{}
	for t, w := range s.wins {
		r.Wins[t] = w
	}
	s.phase = PhaseOver
	s.result = &r
	return GameOver{Result: r}
}
