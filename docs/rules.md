# Jackpot: game rules (backend source of truth)

Status markers:
- **Decided**: stated by the game's author.
- **Provisional**: author leans this way, not final.
- **Assumed**: inferred while writing this doc. Needs confirmation.
- **Open**: not decided yet.

Anything not marked is Decided.

---

## 1. Setup

- 2 to 5 teams, 2 players each (4 to 10 players).
- 4 card types.
- Every player starts with one card of each type. One player also gets an extra card of one type, so they start with 5 cards. That extra card is the **passing card**.
- Copies per type: 11, which covers the 5-team maximum. Cards not dealt form the **pool**.
- Before round 1, each team gets a **private chat for 2 minutes** to agree a signalling code. *(Assumed: once per game, not per round.)*

## 2. Passing

- There is exactly one passing card in play. Whoever holds it has 5 cards.
- The holder passes one card to the next player. Typically they pass a card of a type they hold the fewest of.
- **Hold timer:** after 60 seconds, the server passes a card for the holder: the one whose type the holder has the fewest copies of. Ties are broken randomly.
- **Open:** ring order (how players and partners are seated around the ring).

## 3. Winning a round

- Hold 4 cards of the same type.
- Your **partner** proclaims "Jackpot". You can never proclaim for yourself.
- A proclamation is **rejected, with no penalty**, while the partner of the winner holds the passing card (has 5 cards).
- At any other time a proclamation is accepted. If it's **false** (the proclaimer's partner doesn't hold 4 of a kind), the proclaimer **loses one suspicion point**.
- **Provisional:** a false proclamation made at 0 points removes the proclaimer's right to proclaim for the rest of the game. The alternative is a separate points pool for proclamations. Not decided.
- A winning team earns the next letter of J-A-C-K-P-O-T (one round win = one letter).
- **Open:** what happens between rounds (redeal, and whether suspicion points refresh each round or last the whole game).

## 4. Suspicion

- Every player has **3 suspicion points**, per game as far as stated. Once they're used up you can't suspect anyone.
- You can suspect **anyone at any time**, including your own partner (allowed, but a bad idea) and players who don't hold 4 of a kind.
- The **suspector always loses the point**, whether or not the suspicion was right.
- The suspected player must immediately show cards of different types:
  - If they hold **4 cards**: show **2** different types.
  - If they hold **5 cards**: show **3** different types.
  - This is judged on the hand **as of the suspicion's position in the event log**. If the player passed one millisecond before the suspicion, they hold 4 cards and show 2.
  - A 5-card hand with 3+2 has only 2 types, so it fails. This is intended.
- Shown cards are **revealed to the room**. Leaking hand info is part of the game.
- *(Assumed: the suspected player chooses which cards to show, with a server-side check and a timeout fallback.)*
- If they can't show enough different types, they **reset**:
  - Their cards go back to the pool and they get a fresh hand of one card of each type.
  - If they hold the passing card, they **pass it to the next player first, then reset**. With 5 cards of one type, they pass one and reset the rest.
  - **Open:** which card is passed in this forced pass (player's choice, or the server's rule above).
- If the partner's proclamation is processed **before** the suspicion, the team wins and the suspicion no longer matters.

## 5. Signalling

- Players send gestures from a **fixed vocabulary**. Real signals and fakes use the same mechanism, and the server can't tell them apart.
- **Visibility is rolled once per signal:**
  - 20% of signals go to the sender's partner only (`team` scope).
  - 80% go to everyone, including the partner (`room` scope).
  - Both numbers should be config values.
- **Open:** the gesture vocabulary itself.

## 6. Concurrency

- Each room has a **single ordered event queue**. Whichever event the server processes first wins. This replaces the in-person vote.
- A short **collection window** of roughly 100 to 200 ms, with a **random tie-break** for near-simultaneous events. This is a tunable starting value, not a final one.

## 7. Ending the game

- First team to **7 round wins** (J-A-C-K-P-O-T) wins.
- Or the game ends early if a **majority of teams** agree. A team counts as agreeing only when **both partners** agree.
- At early end, the team with the most wins wins. A tie is a **drawn game**: no winner.

## 8. Dropping out

- A team can drop out at any time. After a **timeout**, if the leaving player holds the passing card it's passed on, and the **whole team is removed** (both partners). The remaining teams continue.
- **Open:** the timeout length, what happens to the removed team's cards, and what happens if only one team remains. Disconnects are assumed to follow the same path.

## 9. Stats and leaderboards

- **MVPs:** players who win mostly by proclaiming, or mostly by holding the 4 cards.
- **Legendary duos:** partners with many wins together.
- This means every round win must record who **proclaimed** and who **held the cards**, and the team.
- **Open:** accounts and persistence, which are needed for leaderboards.

---

## Known risks (not rules)

- **Pool exhaustion:** resets return cards of one type but need one of each type from the pool. With 11 per type and many resets, the pool can run short of some types. Resets are expected to be rare, but the engine should handle an empty pool explicitly instead of failing, and keep a per-type card count invariant.
- **Latency:** the ordered queue favours players with better connections, which is why the collection window exists.
