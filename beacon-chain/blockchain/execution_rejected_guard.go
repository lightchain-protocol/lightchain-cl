package blockchain

// LightChain: this whole file is fork code. It is the duty guard for finding S2 of the 2026-11-20 chain code audit:
// on 2026-11-19/20 a faulty execution client answered INVALID for valid blocks. A rejected block is never inserted,
// so the beacon node kept a stale head that looked valid and synced, and its validators attested and proposed on it,
// which forked the chain. While the guard is active the validator RPC refuses block production and attestation data.

import (
	"github.com/OffchainLabs/prysm/v7/beacon-chain/p2p"
	"github.com/OffchainLabs/prysm/v7/config/features"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/sirupsen/logrus"
)

// LightChain: executionRejectedGuardSlots is how long, counted from the slot of the rejected block, the guard holds on
// the rejection alone. After that it holds only while a third or more of the connected peers report a head slot above
// ours (executionRejectedPeersAhead), that is while the network has moved past our head.
//
// Why the second condition: one block that really is invalid (a faulty or malicious proposer, for example a host whose
// execution client builds on a wrong state, audit H1) is rejected by every honest node, so every honest node would be
// guarded, nobody would propose, and no later block could ever become head to clear the guard: a halt of the whole
// network. With the peer condition that case costs at most this many slots, because honest peers are then stuck at
// the same head as we are. In the 2026-11-20 case (a faulty local execution client, healthy nodes still building) the
// healthy peers are ahead of us, so the guard stays on until our execution client accepts the chain again.
//
// 32 slots (64 s at 2 s slots) is well above the peer status refresh interval, so peer heads are current when the
// rejection-only window ends.
const executionRejectedGuardSlots = primitives.Slot(32)

// LightChain: recordExecutionRejectedSlot remembers the highest slot for which the execution engine returned
// INVALID. Safe for concurrent use.
func (s *Service) recordExecutionRejectedSlot(slot primitives.Slot) {
	for {
		old := s.executionRejectedSlot.Load()
		if uint64(slot) <= old || s.executionRejectedSlot.CompareAndSwap(old, uint64(slot)) {
			return
		}
	}
}

// LightChain: ExecutionRejectedNewerThanHead returns true while the execution engine has rejected (INVALID) a block
// at a slot greater than the head slot, and either that block is at most executionRejectedGuardSlots old or a third
// or more of the connected peers are ahead of our head. It clears as soon as a block at the rejected slot or later
// becomes head. It always returns false when --allow-duties-with-el-rejecting-head is set. It logs one warning per
// epoch while true. Safe for concurrent use.
func (s *Service) ExecutionRejectedNewerThanHead() bool {
	if features.Get().AllowDutiesWithELRejectingHead {
		return false
	}
	rejected := primitives.Slot(s.executionRejectedSlot.Load())
	if rejected == 0 {
		return false
	}
	headSlot := s.HeadSlot()
	if rejected <= headSlot {
		return false
	}
	currentSlot := s.CurrentSlot()
	if currentSlot > rejected+executionRejectedGuardSlots && !s.executionRejectedPeersAhead(headSlot) {
		return false
	}
	// Stored as epoch+1 so that the zero value means "never warned".
	warnEpoch := uint64(slots.ToEpoch(currentSlot)) + 1
	if s.executionRejectedWarnEpoch.Swap(warnEpoch) != warnEpoch {
		log.WithFields(logrus.Fields{
			"headSlot":     headSlot,
			"rejectedSlot": rejected,
			"currentSlot":  currentSlot,
		}).Warn("Execution engine rejected a block newer than the head: refusing block production and attestation data. " +
			"Check the execution client; --allow-duties-with-el-rejecting-head disables this guard")
	}
	return true
}

// LightChain: executionRejectedPeersAhead returns true when at least one connected peer, and at least a third of the
// connected peers with a known status, report a head slot above headSlot. The p2p service passed to the chain service
// in production also provides the peer set; without it (tests, or a p2p service without peers) this returns false,
// so the guard then ends after executionRejectedGuardSlots.
func (s *Service) executionRejectedPeersAhead(headSlot primitives.Slot) bool {
	if s.cfg == nil {
		return false
	}
	pp, ok := s.cfg.P2P.(p2p.PeersProvider)
	if !ok {
		return false
	}
	status := pp.Peers()
	if status == nil {
		return false
	}
	known, ahead := 0, 0
	for _, pid := range status.Connected() {
		cs, err := status.ChainState(pid)
		if err != nil || cs == nil {
			continue
		}
		known++
		if cs.HeadSlot > headSlot {
			ahead++
		}
	}
	return ahead > 0 && 3*ahead >= known
}
