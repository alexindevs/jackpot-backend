# Jackpot: stories & anecdotes

A running log of real-life moments from playing the game. These aren't specs, but they explain *why* some rules exist, so the backend can respect the spirit of the game and not only the letter.

Add new entries at the bottom. Keep names optional.

---

## The "I said it first" problem

**Where it comes from:** real tabletop games.

When two people call out at the same moment (a suspicion and a proclamation, or two proclamations), both usually swear they were first. In person, the table settles it by **vote**, and fights have come out of it.

**What it means for the backend:** there's no table to vote, so the server settles it with a **single ordered event queue per room**. Whichever event the server processes first wins.

**Open design note:** server arrival order favours players with better connections. If that turns out to feel unfair, consider a short collection window with a random tie-break. (Not decided.)

---

## Template for new entries

```
## <short title>

**Where it comes from:** <who / when / what kind of game>

<what happened>

**What it means for the backend:** <rule it explains, or edge case it exposes>
```

---

## "Jackpot!" by Deborah AdeOluwa (Sep 26, 2025)

**Where it comes from:** a first-person account of playing Jackpot in a Nigerian secondary school, using Whot cards. Credit to the author. This is a paraphrase, not the original text. *(Source link to be added.)*

Their version differs from ours in places: 8 players in 4 pairs, 4 cards each, one person picking from a shuffled pile to start the passing, and a false call or a wrong accusation is punished by swapping cards, not by losing points. The spirit is the same: you secretly agree a sign with a partner who sits away from you, your partner is the one who shouts "Jackpot!", and opponents who spot your sign make you drop your perfect four.

Things worth keeping in mind:

- **Partners sit apart.** Pairs deliberately don't sit close, which is where our "never next to your partner" rule comes from.
- **Subtle beats clever.** One player was prized as a partner for being so calm that someone on her team always ended up calling it. One who laughed constantly was the worst partner, because the laughing gave the win away.
- **Bait.** One player staged fake "perfect four" behaviour to get called out, then showed two different cards, and the accuser took the punishment. This is the fake-signal and wasted-suspicion mechanic.
- **Changing signs mid-game.** When opponents caught on, partners improvised a new sign on the spot.
- **Distractions.** Dropped pens, rules arguments, loud complaints, accusing someone of cheating, anything to pull attention away while a signal went out.
- **Signals fail.** The author's sign was complaining about something. Their partner answered by offering her own water instead of shouting "Jackpot!".

**What it means for the backend:**
- Per-round private team chat fits, but this account shows partners also **re-coordinating mid-round**. With our locked chat they could only do that through the signals themselves.
- The distractions suggest a **public social channel** (chat or emotes) may matter to the game's feel. This is not decided.
- A hidden tell like a laugh has no digital equivalent. Timing and hesitation may become the tells instead.
