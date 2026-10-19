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
