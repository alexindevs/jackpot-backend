package botname

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func TestIDsAreBotIDsAndNothingElseIs(t *testing.T) {
	for _, n := range names {
		id := ID(n.Name)
		if !IsBotID(id) || strings.ToLower(string(id)) != string(id) {
			t.Errorf("%s -> %q", n.Name, id)
		}
	}
	for _, id := range []engine.PlayerID{"12345", "dev:alice", "alice", "robot", ""} {
		if IsBotID(id) {
			t.Errorf("%q must not be a bot id", id)
		}
	}
}

func TestEveryNameMakesADistinctID(t *testing.T) {
	seen := map[engine.PlayerID]string{}
	for _, n := range names {
		id := ID(n.Name)
		if prev, dup := seen[id]; dup {
			t.Errorf("%s and %s share the id %q", prev, n.Name, id)
		}
		seen[id] = n.Name
		if n.Name == "" || n.Origin == "" || strings.ContainsAny(n.Name, " :'-") {
			t.Errorf("bad entry %+v", n)
		}
	}
}

func TestPickNeverRepeatsAndRunsOut(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	taken := map[engine.PlayerID]bool{}
	for i := 0; i < Count(); i++ {
		id, ok := Pick(rng, taken)
		if !ok || taken[id] {
			t.Fatalf("pick %d gave %q, ok=%v", i, id, ok)
		}
		taken[id] = true
	}
	if _, ok := Pick(rng, taken); ok {
		t.Error("with every name taken there is nothing to pick")
	}
}

func TestProfileHasANameAndALookThatNeverChanges(t *testing.T) {
	id := ID("Adetula")
	name, style, ok := Profile(id)
	if !ok || name != "Adetula" {
		t.Fatalf("profile = %q, %v", name, ok)
	}
	var s map[string]string
	if err := json.Unmarshal(style, &s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"skinColor", "primaryColor", "hairColor", "hair", "hat"} {
		if s[k] == "" {
			t.Errorf("style has no %s: %v", k, s)
		}
	}
	if _, again, _ := Profile(id); string(again) != string(style) {
		t.Error("a bot must always look the same")
	}
	if _, _, ok := Profile("12345"); ok {
		t.Error("a person has no bot profile")
	}
}
