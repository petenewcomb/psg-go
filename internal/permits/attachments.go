// Copyright (c) Peter Newcomb. All rights reserved.
// Licensed under the MIT License.

package permits

import (
	"github.com/petenewcomb/streampool/internal/dll"
	"github.com/petenewcomb/streampool/internal/omnipool"
)

// Attachments is a wave's per-pool discovery anchor: the two structures that
// make a parked body's or a postponed admission's units discoverable to the
// wave's nested subtree — pump episodes and registration attachments. It is
// pure discovery: no accounting lives here (bodies hold permits, never
// waves), and every read or edit runs under the owning pool's ledger mutex,
// preserving the single-mutex walk discipline. Lazily created at first
// attachment; empty by settlement before the wave can reach Done
// (WORKING_NOTES teardown settlement, item 6).
type Attachments struct {
	// episodes are parked bodies' standing loan offers on this wave: each
	// lender parked pumping the wave (nested drain and cross-wave submit
	// alike). An episode dies with its park.
	episodes dll.List[*Episode]

	// registrations are postponed admissions' standing reservations, keyed
	// by the registered demand. A registration attachment dies atomically
	// with its demand's retirement or departure.
	registrations dll.List[*registrationAttachment]
}

// Empty reports whether nothing is attached — the wave-teardown assert:
// settlement empties attachments before Done can fire, so a non-empty
// anchor at teardown is a departure bug. The owning pool's mutex must be
// held.
func (at *Attachments) Empty() bool {
	return at.episodes.Front() == nil && at.registrations.Front() == nil
}

// Episode is a parked body's loan offer on a pumped wave: the discoverable
// face of its park-as-reserved units. The park frame holds the episode
// pointer (recall and claim are claimant-directed; the episode adds no
// forest edge), and the episode ends with the park — closing it detaches
// discovery, while any loans it brokered survive on the lender's account
// under the ordinary revocable-loan rules.
type Episode struct {
	dll.Links[*Episode]

	// lender is the parked body's account, home of the lendable reservation
	// and of the receivable for whatever gets borrowed through this episode.
	lender *Account
}

var episodePool = omnipool.For[Episode]()

// Reset implements [omnipool.Resetter].
func (e *Episode) Reset() { e.lender = nil }

// registrationAttachment links a registered demand into a wave's anchor.
// A separate node rather than a second set of links on Demand: the demand's
// own links hold its place in the pool's demand queue for its whole
// registration, and the attachment is a per-wave overlay with its own
// lifetime.
type registrationAttachment struct {
	dll.Links[*registrationAttachment]

	demand *Demand
}

var registrationAttachmentPool = omnipool.For[registrationAttachment]()

// Reset implements [omnipool.Resetter].
func (ra *registrationAttachment) Reset() { ra.demand = nil }

// openEpisode attaches a parked lender's loan offer to the anchor. mu must
// be held; the returned episode belongs to the park frame and must be
// closed at unwind.
func (l *ledger) openEpisode(at *Attachments, lender *Account) *Episode {
	e := episodePool.Get()
	e.lender = lender
	at.episodes.PushBack(e)
	return e
}

// closeEpisode ends a park's episode: discovery detaches; brokered loans
// stay on the lender's account for the returning owner's claim to settle.
// mu must be held.
func (l *ledger) closeEpisode(at *Attachments, e *Episode) {
	at.episodes.Remove(e)
	episodePool.Release(e)
}

// attachRegistration makes a postponed admission's standing reservation
// discoverable on its wave. mu must be held; the returned node dies with
// the registration via detachRegistration.
func (l *ledger) attachRegistration(at *Attachments, d *Demand) *registrationAttachment {
	ra := registrationAttachmentPool.Get()
	ra.demand = d
	at.registrations.PushBack(ra)
	return ra
}

// detachRegistration removes a registration attachment — in the same mutex
// section as the demand's retirement or departure, so no transiently
// discoverable departed lender exists. mu must be held.
//
//nolint:unused // attachRegistration's pair; the cutover's departure path is its caller
func (l *ledger) detachRegistration(at *Attachments, ra *registrationAttachment) {
	at.registrations.Remove(ra)
	registrationAttachmentPool.Release(ra)
}

// borrowFromAnchor drains up to need units from the anchor's attached
// sources for a beneficiary, in the drain discipline's behind-source order:
// registered demands' reservations first, then parked bodies' (episodes'),
// tail-first within each — deepest in service order drains first, costing
// its holder nothing in delivery order. Loans are recorded on each source's
// account (revocable until the borrower starts; recalled by the returning
// owner's claim through the ordinary machinery). mu must be held.
func (l *ledger) borrowFromAnchor(at *Attachments, beneficiary *Account, need uint64) (borrowed uint64) {
	for ra := at.registrations.Back(); need > borrowed && ra != nil; ra = at.registrations.Prev(ra) {
		borrowed += l.borrow(ra.demand.account, beneficiary, need-borrowed)
	}
	for e := at.episodes.Back(); need > borrowed && e != nil; e = at.episodes.Prev(e) {
		borrowed += l.borrow(e.lender, beneficiary, need-borrowed)
	}
	if borrowed > 0 {
		l.refreshAnchor() // drained demands' shortfalls reopened
	}
	return borrowed
}

// shiftDebt is normalization's one move: reassign up to w units of an inner
// lender's receivable to an ancestor with standing margin — the ancestor's
// margin makes the inner lender whole and the ancestor becomes the
// lender-of-record (L.lent shrinks, U.lent grows; a debt reassignment,
// never a fresh grant, so no phantom debt can leak into teardown).
// Normalization is emergent, not an operation: chain walks call this on
// their unwind wherever inner debt and ancestor margin coexist. mu must be
// held.
func (l *ledger) shiftDebt(inner, ancestor *Account, w uint64) uint64 {
	take := min(w, inner.lent, ancestor.reserved.Load())
	if take == 0 {
		return 0
	}
	// The drain settles inner's receivable by the fungible arrival rule;
	// re-recording it on the ancestor completes the reassignment with the
	// pool-wide loan count unchanged.
	l.drain(ancestor, inner, take)
	ancestor.lent += take
	l.loansOutstanding.Add(int64(take)) //nolint:gosec // G115: take bounded by validated weights
	return take
}
