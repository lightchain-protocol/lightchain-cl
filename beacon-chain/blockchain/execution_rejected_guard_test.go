package blockchain

// LightChain: this whole file is fork code: tests for the engine-INVALID error classification (audit S1, S4) and the
// duty guard (audit S2) in execution_rejected_guard.go.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/execution"
	mockExecution "github.com/OffchainLabs/prysm/v7/beacon-chain/execution/testing"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/p2p"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/p2p/peers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/p2p/peers/scorers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/verification"
	"github.com/OffchainLabs/prysm/v7/config/features"
	"github.com/OffchainLabs/prysm/v7/config/params"
	consensusblocks "github.com/OffchainLabs/prysm/v7/consensus-types/blocks"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/encoding/bytesutil"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/testing/util"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pkg/errors"
)

func TestIsEngineInvalidBlock(t *testing.T) {
	engine := []struct {
		name string
		err  error
	}{
		{"sentinel", ErrInvalidPayload},
		{"notifyNewPayload form", invalidBlock{error: ErrInvalidPayload, lastValidHash: [32]byte{'a'}}},
		{"pruneInvalidBlock form", invalidBlock{error: ErrInvalidPayload, invalidAncestorRoots: [][32]byte{{'b'}}, lastValidHash: [32]byte{'a'}}},
		{"forkchoiceUpdated form", invalidBlock{error: ErrInvalidPayload, root: [32]byte{'c'}}},
		{"wrapped as ReceiveBlock does", errors.Wrap(errors.Wrap(invalidBlock{error: ErrInvalidPayload}, "could not notify the engine of the new payload"), "validator execution and consensus")},
	}
	for _, c := range engine {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, true, IsEngineInvalidBlock(c.err))
			// Existing invalid-block handling must still see it, so fork-choice pruning and callers are unchanged.
			require.Equal(t, true, IsInvalidBlock(c.err))
			require.Equal(t, true, errors.Is(c.err, verification.ErrInvalid))
		})
	}

	other := []struct {
		name    string
		err     error
		invalid bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("received an INVALID payload from execution engine"), false},
		{"state transition failure", invalidBlock{error: errors.New("bad state root")}, true},
		{"invalid block hash", ErrInvalidBlockHashPayloadStatus, true},
		{"not descendant of finalized", invalidBlock{error: ErrNotDescendantOfFinalized, root: [32]byte{'d'}}, true},
		{"blacklisted", errBlacklistedRoot, false},
		{"undefined engine error", errors.WithMessage(ErrUndefinedExecutionEngineError, "timeout"), false},
	}
	for _, c := range other {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, false, IsEngineInvalidBlock(c.err))
			require.Equal(t, c.invalid, IsInvalidBlock(c.err))
		})
	}
}

// guardTestService returns a service whose head block is at headSlot and whose clock is in the middle of currentSlot.
func guardTestService(t *testing.T, headSlot, currentSlot primitives.Slot) *Service {
	s := &Service{cfg: &config{}}
	setGuardTestHead(t, s, headSlot)
	start, err := slots.StartTime(time.Unix(0, 0), currentSlot)
	require.NoError(t, err)
	s.genesisTime = time.Now().Add(-start.Sub(time.Unix(0, 0)) - params.BeaconConfig().SlotDuration()/2)
	require.Equal(t, currentSlot, s.CurrentSlot())
	return s
}

func setGuardTestHead(t *testing.T, s *Service, slot primitives.Slot) {
	st, err := util.NewBeaconState()
	require.NoError(t, err)
	b := util.NewBeaconBlock()
	b.Block.Slot = slot
	sb, err := consensusblocks.NewSignedBeaconBlock(b)
	require.NoError(t, err)
	s.headLock.Lock()
	s.head = &head{block: sb, state: st, slot: slot}
	s.headLock.Unlock()
	require.Equal(t, slot, s.HeadSlot())
}

func TestExecutionRejectedNewerThanHead_HeadMovement(t *testing.T) {
	s := guardTestService(t, 10, 12)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "no rejection recorded")

	s.recordExecutionRejectedSlot(9)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "rejected block older than the head")

	s.recordExecutionRejectedSlot(12)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "rejected block newer than the head")

	s.recordExecutionRejectedSlot(11)
	require.Equal(t, uint64(12), s.executionRejectedSlot.Load(), "a lower slot must not replace the highest")
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead())

	setGuardTestHead(t, s, 11)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "head moved but is still older than the rejected block")

	setGuardTestHead(t, s, 12)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "a valid block at the rejected slot became head")

	s.recordExecutionRejectedSlot(14)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "a newer rejection arms the guard again")

	setGuardTestHead(t, s, 15)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "a valid block past the rejected slot became head")
}

func TestExecutionRejectedNewerThanHead_FlagDisables(t *testing.T) {
	s := guardTestService(t, 10, 12)
	s.recordExecutionRejectedSlot(12)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead())

	reset := features.InitWithReset(&features.Flags{AllowDutiesWithELRejectingHead: true})
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "--allow-duties-with-el-rejecting-head disables the guard")
	reset()

	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "guard is on by default")
}

// peersOnlyP2P is a p2p accessor that only provides a peer set.
type peersOnlyP2P struct {
	p2p.Accessor
	status *peers.Status
}

func (p *peersOnlyP2P) Peers() *peers.Status { return p.status }

func guardTestPeers(t *testing.T, headSlots ...primitives.Slot) *peersOnlyP2P {
	status := peers.NewStatus(t.Context(), &peers.StatusConfig{ScorerParams: &scorers.Config{}})
	for i, hs := range headSlots {
		pid := peer.ID(fmt.Sprintf("peer%d", i))
		status.Add(nil, pid, nil, network.DirOutbound)
		status.SetConnectionState(pid, peers.Connected)
		status.SetChainState(pid, &ethpb.StatusV2{HeadSlot: hs})
	}
	return &peersOnlyP2P{status: status}
}

func TestExecutionRejectedNewerThanHead_WindowAndPeers(t *testing.T) {
	const headSlot = primitives.Slot(10)
	const rejected = primitives.Slot(11)

	// Inside the window the rejection alone holds the guard, whatever the peers say.
	s := guardTestService(t, headSlot, rejected+executionRejectedGuardSlots)
	s.recordExecutionRejectedSlot(rejected)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "inside the window, no peers")
	s.cfg.P2P = guardTestPeers(t, headSlot, headSlot, headSlot)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "inside the window, peers at our head")

	// After the window the guard holds only while a third or more of the peers are ahead of our head.
	s = guardTestService(t, headSlot, rejected+executionRejectedGuardSlots+1)
	s.recordExecutionRejectedSlot(rejected)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "after the window, no peer information")

	s.cfg.P2P = guardTestPeers(t, headSlot, headSlot, headSlot, 40)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "after the window, one peer of four ahead (a lone faulty proposer)")

	s.cfg.P2P = guardTestPeers(t, headSlot, headSlot, 40)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "after the window, one peer of three ahead")

	s.cfg.P2P = guardTestPeers(t, headSlot, 40, 41, 42)
	require.Equal(t, true, s.ExecutionRejectedNewerThanHead(), "after the window, the network moved past our head")

	s.cfg.P2P = guardTestPeers(t, headSlot, headSlot)
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead(), "after the window, the whole network is at our head")

	// The flag wins over the peer condition too.
	s.cfg.P2P = guardTestPeers(t, 40, 41, 42)
	reset := features.InitWithReset(&features.Flags{AllowDutiesWithELRejectingHead: true})
	defer reset()
	require.Equal(t, false, s.ExecutionRejectedNewerThanHead())
}

// When the consensus check and the engine both fail, the consensus error is returned, so the sync services still cache
// the block as bad and downscore: an engine INVALID must be reported only when it is the only reason.
func TestValidateExecutionAndConsensus_PrefersConsensusError(t *testing.T) {
	service, _ := minimalTestService(t)
	service.cfg.ExecutionEngineCaller = &mockExecution.EngineClient{ErrNewPayload: execution.ErrInvalidPayloadStatus}
	preState, _ := util.DeterministicGenesisStateBellatrix(t, 2)
	b := util.NewBeaconBlockBellatrix()
	b.Block.Slot = 1
	b.Block.ParentRoot = bytesutil.PadTo([]byte("parent not in forkchoice"), 32)
	b.Block.Body.ExecutionPayload.BlockNumber = 1
	sb, err := consensusblocks.NewSignedBeaconBlock(util.HydrateSignedBeaconBlockBellatrix(b))
	require.NoError(t, err)
	rob, err := consensusblocks.NewROBlock(sb)
	require.NoError(t, err)

	// The engine path ran and rejected the block ...
	for i := 0; i < 20; i++ {
		_, _, err = service.validateExecutionAndConsensus(t.Context(), preState, rob)
		require.NotNil(t, err)
		// ... but the error is the consensus one, every time, whichever goroutine finished first.
		require.Equal(t, true, errors.Is(err, ErrNotDescendantOfFinalized))
		require.Equal(t, true, IsInvalidBlock(err))
		require.Equal(t, false, IsEngineInvalidBlock(err))
	}
	require.Equal(t, uint64(1), service.executionRejectedSlot.Load(), "the engine verdict is still recorded for the duty guard")
}

func TestRecordExecutionRejectedSlot_Concurrent(t *testing.T) {
	s := guardTestService(t, 0, 1)
	var wg sync.WaitGroup
	for i := 1; i <= 200; i++ {
		wg.Add(1)
		go func(slot primitives.Slot) {
			defer wg.Done()
			s.recordExecutionRejectedSlot(slot)
			_ = s.ExecutionRejectedNewerThanHead()
		}(primitives.Slot(i))
	}
	wg.Wait()
	require.Equal(t, uint64(200), s.executionRejectedSlot.Load())
}
