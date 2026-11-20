package validator

// LightChain: this whole file is fork code: the validator RPC refuses block production and attestation data while the
// execution engine has rejected a block newer than the head (audit S2 duty guard).

import (
	"testing"
	"time"

	mock "github.com/OffchainLabs/prysm/v7/beacon-chain/blockchain/testing"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/cache"
	doublylinkedtree "github.com/OffchainLabs/prysm/v7/beacon-chain/forkchoice/doubly-linked-tree"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/rpc/core"
	mockSync "github.com/OffchainLabs/prysm/v7/beacon-chain/sync/initial-sync/testing"
	"github.com/OffchainLabs/prysm/v7/config/params"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const elRejectedMsg = "rejected a block newer than the head"

func TestServer_GetBeaconBlock_ExecutionRejectedNewerThanHead(t *testing.T) {
	params.SetupTestConfigCleanup(t)
	cfg := params.BeaconConfig().Copy()
	cfg.BellatrixForkEpoch = 2
	cfg.AltairForkEpoch = 1
	params.OverrideBeaconConfig(cfg)

	bellatrixSlot, err := slots.EpochStart(params.BeaconConfig().BellatrixForkEpoch)
	require.NoError(t, err)

	mockChainService := &mock.ChainService{ForkChoiceStore: doublylinkedtree.New()}
	proposerServer := &Server{
		OptimisticModeFetcher: &mock.ChainService{ExecutionRejectedNewer: true},
		SyncChecker:           &mockSync.Sync{},
		ForkFetcher:           mockChainService,
		ForkchoiceFetcher:     mockChainService,
		TimeFetcher:           &mock.ChainService{}}
	_, err = proposerServer.GetBeaconBlock(t.Context(), &ethpb.BlockRequest{Slot: bellatrixSlot + 1})
	s, ok := status.FromError(err)
	require.Equal(t, true, ok)
	require.DeepEqual(t, codes.Unavailable, s.Code())
	require.ErrorContains(t, elRejectedMsg, err)
	require.ErrorContains(t, "--allow-duties-with-el-rejecting-head", err)
}

func TestGetAttestationData_ExecutionRejectedNewerThanHead(t *testing.T) {
	params.SetupTestConfigCleanup(t)
	cfg := params.BeaconConfig().Copy()
	cfg.BellatrixForkEpoch = 0
	params.OverrideBeaconConfig(cfg)

	newServer := func(guarded bool, attCache *cache.AttestationDataCache) *Server {
		return &Server{
			SyncChecker:           &mockSync.Sync{},
			OptimisticModeFetcher: &mock.ChainService{ExecutionRejectedNewer: guarded},
			TimeFetcher:           &mock.ChainService{Genesis: time.Now()},
			CoreService: &core.Service{
				GenesisTimeFetcher:    &mock.ChainService{Genesis: time.Now()},
				HeadFetcher:           &mock.ChainService{},
				AttestationCache:      attCache,
				OptimisticModeFetcher: &mock.ChainService{ExecutionRejectedNewer: guarded},
			},
		}
	}

	// A cached result for the requested slot is served when not guarded ...
	attCache := cache.NewAttestationDataCache()
	require.NoError(t, attCache.Put(&cache.AttestationConsensusData{Slot: 0, HeadRoot: make([]byte, 32)}))
	res, err := newServer(false, attCache).GetAttestationData(t.Context(), &ethpb.AttestationDataRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(0), uint64(res.Slot))

	// ... and refused while guarded, cached or not.
	for _, c := range []*cache.AttestationDataCache{attCache, cache.NewAttestationDataCache()} {
		_, err = newServer(true, c).GetAttestationData(t.Context(), &ethpb.AttestationDataRequest{})
		s, ok := status.FromError(err)
		require.Equal(t, true, ok)
		require.DeepEqual(t, codes.Unavailable, s.Code())
		require.ErrorContains(t, elRejectedMsg, err)
	}
}
