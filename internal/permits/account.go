// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"sync/atomic"

	"github.com/petenewcomb/streampool/internal/dll"
	"github.com/petenewcomb/streampool/internal/omnipool"
	"github.com/petenewcomb/streampool/internal/rdvq"
)

// Account is a party's per-pool ledger object in the permit forest: the home
// of its reservation, the record of its receivables, and a link in its
// descendants' credit-discovery chains. An account exists exactly while its
// party's state must be discoverable by others — as a lender, a standing
// registrant, or a link in someone else's chain
// (docs/plan/forest-severability.md, "The lazy forest"); a happy admission's
// reservation stays handle-local and never touches the forest.
//
// An account is open until settlement closes it — a terminal transition made
// only under the pool mutex, so a mutex holder never sees an account close
// mid-walk. A closed account's ledger is zero, terminally; that is the whole
// reason money never routes through closed territory. A nil parent means
// nothing live above: a root while open, ancestry-closed once closed.
type Account struct {
	// GenRefCounter owns the account's memory. Two ref kinds exist: the
	// owner's lifetime ref (armed by the pool's Get, released at settlement's
	// end once the meta chain carries it — docs/plan/forest-severability.md
	// §Memory and refs) and child parent-edge refs (created at install or
	// splice, released at splice or sever, all under the pool mutex).
	omnipool.GenRefCounter

	// pool is the account's forest; set at creation, constant thereafter.
	pool *Pool

	// parent is the account's edge toward the forest root. Writers: the
	// creator (pre-publication) and pool-mutex holders (splice, sever). A
	// claim that finds its parent closed splices past it under the mutex —
	// once per closure per child (docs/plan/forest-severability.md
	// §Synchronization).
	parent *Account

	// reserved is the account's reservation balance: units set aside for its
	// party, not lent, not in use. The claim gate consumes it by gated CAS —
	// the one lock-free word of the money model; every other movement runs
	// under the pool mutex (docs/plan/directed-delivery.md §Vocabulary,
	// reservation-mechanics item 2).
	reserved atomic.Uint64

	// lent is the account's receivable: units currently backing borrowers'
	// reservations elsewhere. Written only under the pool mutex. A receivable
	// ends by repayment (delivery tier 2) or by departure abandonment —
	// never by recall from the borrower's side.
	lent uint64

	// closed latches settlement, terminally. Stored only inside pool-mutex
	// sections (frozen liveness for walkers); read with an acquire load on
	// the lock-free happy-claim parent check.
	closed atomic.Bool
}

// accountPool recycles Accounts. Get arms refs=1 (the owner's lifetime ref);
// recycle at refs==0 is purely structural — settlement already routed every
// unit before closing.
var accountPool = omnipool.For[Account]()

// Reset implements [omnipool.Resetter]: a recycled account must already be
// settled — closing is the last act of settlement, and the closing store
// zeroes nothing because nothing may remain. Residue here is a departure or
// abandonment bug at the exact seam, so it panics
// (docs/plan/forest-severability.md §Verification obligations).
func (a *Account) Reset() {
	if !a.closed.Load() {
		panic("permits: account recycled while open")
	}
	if a.reserved.Load() != 0 || a.lent != 0 {
		panic("permits: account recycled with nonzero ledger")
	}
	a.pool = nil
	a.parent = nil
	a.closed.Store(false)
}

// shortfall is the gap between a reservation's balance and its holder's
// weight (docs/plan/directed-delivery.md §Vocabulary).
func (a *Account) shortfall(weight uint64) uint64 {
	r := a.reserved.Load()
	if r >= weight {
		return 0
	}
	return weight - r
}

// claimant is a party blocked at the claim gate — the reserved → in-use
// transition immediately before its body runs: a registered admission whose
// units were drained before it could claim, or a returning parked body whose
// units were lent and not yet repaid. Claimants queue FIFO at the pool;
// deliveries serve them before any demand, because a claim settles only when
// its units come home and a release intercepted by a demand would park the
// claimant behind fresh arrivals forever (docs/plan/directed-delivery.md
// §The delivery order).
type claimant struct {
	// Links is the claimant's membership in the pool's claimant queue.
	// Linked ⇔ blocked at the gate. Guarded by the pool mutex.
	dll.Links[*claimant]

	// account homes the claimant's reservation. A claimant arrives carrying
	// whatever its reservation still holds after loans (its unlent residue),
	// so the claimant queue is never front-loaded at rest and junior
	// claimants hold real units.
	account *Account

	// weight is the whole amount the pending claim needs; the reservation's
	// shortfall against it is what deliveries fill.
	weight uint64

	// waiter is the claimant's direct park point. Its wake is a completion
	// signal — it fires only when the reservation is whole, so the woken
	// retry cannot miss on the reserve side unless a drain intervened
	// (docs/plan/directed-delivery.md §Spill and the anchor).
	waiter rdvq.Waiter
}

// claimantPool recycles claimant frames across gate blocks.
var claimantPool = omnipool.For[claimant]()

// Init implements [omnipool.Initer]: the embedded waiter allocates its wake
// channel once per physical frame.
func (c *claimant) Init() {
	c.waiter.Init()
}

// Reset implements [omnipool.Resetter]: the frame unlinks before release and
// the waiter keeps its channel and generation (see rdvq.Waiter.Reset).
func (c *claimant) Reset() {
	c.account = nil
	c.weight = 0
	c.waiter.Reset()
}
