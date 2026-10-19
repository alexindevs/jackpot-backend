package room

import (
	"testing"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// seqsOf lists the sequence numbers a recipient was given, in order.
func seqsOf(outs []Out, to engine.PlayerID) []int64 {
	var s []int64
	for _, o := range outs {
		if o.To == to && o.Seq != 0 {
			s = append(s, o.Seq)
		}
	}
	return s
}

func TestMessagesCarryAPerRecipientSequenceThatCountsUp(t *testing.T) {
	h := newHarness(t, 1)
	h.joinN(4)
	var last int64
	for _, o := range h.r.logs[h.r.order[0]] {
		if o.Seq != last+1 {
			t.Fatalf("p01 sequence jumped from %d to %d", last, o.Seq)
		}
		last = o.Seq
	}
	if last == 0 || h.r.seqs[h.r.order[0]] != last {
		t.Errorf("seqs = %d, last logged = %d", h.r.seqs[h.r.order[0]], last)
	}
}

func TestStateMessagesAreNotNumbered(t *testing.T) {
	h := newHarness(t, 2)
	h.playing(4)
	for _, o := range h.r.logs[h.r.order[0]] {
		switch o.Msg.(type) {
		case Snapshot, HandUpdate, TimerStarted, Missed:
			t.Fatalf("%T should not be kept for replay", o.Msg)
		}
	}
}

func TestResumeReturnsExactlyWhatWasMissed(t *testing.T) {
	h := newHarness(t, 3)
	h.playing(4)
	a, b := pid(1), pid(2)
	before := h.r.seqs[a]
	h.must(b, Chat{Text: "one"})
	h.must(b, Chat{Text: "two"})
	if h.r.seqs[a] != before+2 {
		t.Fatalf("a should have been sent two more messages, seq %d -> %d", before, h.r.seqs[a])
	}

	outs, err := h.r.Handle(h.now, a, Resume{After: before})
	if err != nil {
		t.Fatal(err)
	}
	ms := msgs[Missed](outs, a)
	if len(ms) != 1 {
		t.Fatalf("got %d Missed replies", len(ms))
	}
	m := ms[0]
	if !m.Complete || m.From != before+1 || m.To != before+2 || len(m.Messages) != 2 {
		t.Fatalf("missed = %+v", m)
	}
	for i, o := range m.Messages {
		if c, ok := o.Msg.(ChatMessage); !ok || o.Seq != before+int64(i)+1 {
			t.Errorf("message %d = %T seq %d", i, o.Msg, o.Seq)
		} else if c.Text != []string{"one", "two"}[i] {
			t.Errorf("message %d text %q", i, c.Text)
		}
	}
	if len(seqsOf(outs, a)) != 0 {
		t.Error("a Missed reply must not itself be numbered")
	}
}

func TestResumeWhenUpToDateIsEmptyAndComplete(t *testing.T) {
	h := newHarness(t, 4)
	h.playing(4)
	outs, _ := h.r.Handle(h.now, pid(1), Resume{After: h.r.seqs[h.r.order[0]]})
	m := msgs[Missed](outs, pid(1))[0]
	if !m.Complete || len(m.Messages) != 0 {
		t.Errorf("missed = %+v", m)
	}
}

func TestResumeSaysSoWhenTheLogNoLongerReachesBack(t *testing.T) {
	h := newHarness(t, 5)
	h.playing(4)
	a, b := pid(1), pid(2)
	start := h.r.seqs[a]
	for i := 0; i < replayLimit+20; i++ {
		h.r.chatRate = map[engine.PlayerID]*bucket{} // not testing the rate limit here
		h.must(b, Chat{Text: "x"})
	}
	if n := len(h.r.logs[a]); n != replayLimit {
		t.Fatalf("log holds %d, want %d", n, replayLimit)
	}
	outs, _ := h.r.Handle(h.now, a, Resume{After: start})
	m := msgs[Missed](outs, a)[0]
	if m.Complete {
		t.Error("a gap must be reported")
	}
	if len(m.Messages) != replayLimit || m.To != h.r.seqs[a] {
		t.Errorf("messages = %d, to = %d", len(m.Messages), m.To)
	}
	// A client that only missed the tail is still complete.
	outs, _ = h.r.Handle(h.now, a, Resume{After: h.r.seqs[a] - 3})
	if m := msgs[Missed](outs, a)[0]; !m.Complete || len(m.Messages) != 3 {
		t.Errorf("tail = %+v", m)
	}
}

func TestOnlyMembersCanResume(t *testing.T) {
	h := newHarness(t, 6)
	h.playing(4)
	h.mustFail("stranger", Resume{After: 0}, ErrNotMember)
}

func TestASnapshotSaysWhereTheSequenceStood(t *testing.T) {
	h := newHarness(t, 7)
	h.playing(4)
	s := h.r.snapshotFor(h.r.order[0])
	if s.Seq != h.r.seqs[h.r.order[0]] {
		t.Errorf("snapshot seq = %d, want %d", s.Seq, h.r.seqs[h.r.order[0]])
	}
}

func TestEachRecipientHasAContiguousSequenceOfTheirOwn(t *testing.T) {
	h := newHarness(t, 8)
	h.playing(4)
	a := h.r.order[0]
	var target engine.PlayerID
	for _, p := range h.r.order {
		if p != a && p != h.partnerOf(a) {
			target = p
		}
	}
	h.must(a, Suspect{Target: target})
	for _, p := range h.r.order {
		for i, o := range h.r.logs[p] {
			if i > 0 && o.Seq != h.r.logs[p][i-1].Seq+1 {
				t.Fatalf("%s: sequence not contiguous at %d", p, i)
			}
		}
	}
	// The target was asked something privately, so it has a message nobody else got.
	if len(msgs[GameEvent](h.r.logs[target], target)) == 0 {
		t.Error("the target's log should hold game events")
	}
}
