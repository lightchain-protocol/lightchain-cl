package sync

// LightChain: this whole file is fork code: a block that failed only because the execution engine answered INVALID is
// not put in the bad-block cache (audit S1); any other invalid block still is.

import (
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/blockchain"
	chainMock "github.com/OffchainLabs/prysm/v7/beacon-chain/blockchain/testing"
	lruwrpr "github.com/OffchainLabs/prysm/v7/cache/lru"
	"github.com/OffchainLabs/prysm/v7/consensus-types/blocks"
	prysmTrace "github.com/OffchainLabs/prysm/v7/monitoring/tracing/trace"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/testing/util"
	gcache "github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
)

// engineInvalidErr is the error ReceiveBlock returns when the engine answers INVALID for a block's payload.
var engineInvalidErr = errors.Wrap(errors.Wrap(blockchain.ErrInvalidPayload, "could not notify the engine of the new payload"), "validator execution and consensus")

// otherInvalidErr is an invalid-block error with another cause (INVALID_BLOCK_HASH, which stays punishable).
var otherInvalidErr = errors.Wrap(blockchain.ErrInvalidBlockHashPayloadStatus, "validator execution and consensus")

func TestBeaconBlockSubscriber_EngineInvalidNotCachedAsBad(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCached bool
	}{
		{name: "engine INVALID", err: engineInvalidErr, wantCached: false},
		{name: "other invalid block", err: otherInvalidErr, wantCached: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Service{
				cfg: &config{
					chain: &chainMock.ChainService{ReceiveBlockMockErr: c.err},
				},
				seenBlockCache: lruwrpr.New(10),
				badBlockCache:  lruwrpr.New(10),
			}
			msg := util.NewBeaconBlock()
			root, err := msg.Block.HashTreeRoot()
			require.NoError(t, err)

			err = s.beaconBlockSubscriber(t.Context(), msg)
			require.ErrorIs(t, err, c.err)
			require.Equal(t, c.wantCached, s.hasBadBlock(root))
			if c.wantCached {
				require.Equal(t, 1, len(s.badBlockCache.Keys()))
			} else {
				require.Equal(t, 0, len(s.badBlockCache.Keys()))
			}
		})
	}
}

func TestHandleBlockProcessingError_EngineInvalidNotCachedAsBad(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCached bool
	}{
		{name: "engine INVALID", err: errors.Wrap(engineInvalidErr, "receive block"), wantCached: false},
		{name: "other invalid block", err: errors.Wrap(otherInvalidErr, "receive block"), wantCached: true},
		{name: "not an invalid block", err: errors.New("timeout"), wantCached: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Service{
				badBlockCache:       lruwrpr.New(10),
				slotToPendingBlocks: gcache.New(time.Second, 2*time.Second),
				seenPendingBlocks:   make(map[[32]byte]bool),
			}
			b, err := blocks.NewSignedBeaconBlock(util.NewBeaconBlock())
			require.NoError(t, err)
			root, err := b.Block().HashTreeRoot()
			require.NoError(t, err)

			s.handleBlockProcessingError(t.Context(), c.err, b, root)
			require.Equal(t, c.wantCached, s.hasBadBlock(root))

			// A child of the block is condemned with it only when the block is in the cache (the same lookup drives
			// the gossip "invalid parent" REJECT in validateBeaconBlockPubSub).
			child := util.NewBeaconBlock()
			child.Block.Slot = 1
			child.Block.ParentRoot = root[:]
			cb, err := blocks.NewSignedBeaconBlock(child)
			require.NoError(t, err)
			childRoot, err := cb.Block().HashTreeRoot()
			require.NoError(t, err)
			_, span := prysmTrace.StartSpan(t.Context(), "test")
			keepProcessing, err := s.checkIfBlockIsBad(t.Context(), span, 1, cb, childRoot)
			require.NoError(t, err)
			require.Equal(t, !c.wantCached, keepProcessing)
			require.Equal(t, c.wantCached, s.hasBadBlock(childRoot))
		})
	}
}
