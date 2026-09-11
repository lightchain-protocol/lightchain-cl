package epoch_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/epoch"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/time"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/transition"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	state_native "github.com/OffchainLabs/prysm/v7/beacon-chain/state/state-native"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state/stateutil"
	fieldparams "github.com/OffchainLabs/prysm/v7/config/fieldparams"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/testing/assert"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/testing/util"
	"google.golang.org/protobuf/proto"
)

func TestProcessSlashings_NotSlashed(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot:       0,
		Validators: []*ethpb.Validator{{Slashed: true}},
		Balances:   []uint64{params.BeaconConfig().MaxEffectiveBalance},
		Slashings:  []uint64{0, 1e9},
	}
	s, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	require.NoError(t, epoch.ProcessSlashings(s))
	wanted := params.BeaconConfig().MaxEffectiveBalance
	assert.Equal(t, wanted, s.Balances()[0], "Unexpected slashed balance")
}

func TestProcessSlashings_SlashedLess(t *testing.T) {
	tests := []struct {
		state *ethpb.BeaconState
		want  uint64
	}{
		{
			state: &ethpb.BeaconState{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance}},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance, params.BeaconConfig().MaxEffectiveBalance},
				Slashings: []uint64{0, 1e9},
			},
			// penalty    = validator balance / increment * (2*total_penalties) / total_balance * increment
			// 1000000000 = (32 * 1e9)        / (1 * 1e9) * (1*1e9)             / (32*1e9)      * (1 * 1e9)
			want: uint64(31000000000), // 32 * 1e9 - 1000000000
		},
		{
			state: &ethpb.BeaconState{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
				},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance, params.BeaconConfig().MaxEffectiveBalance},
				Slashings: []uint64{0, 1e9},
			},
			// penalty    = validator balance / increment * (2*total_penalties) / total_balance * increment
			// 500000000 = (32 * 1e9)        / (1 * 1e9) * (1*1e9)             / (32*1e9)      * (1 * 1e9)
			want: uint64(32000000000), // 32 * 1e9 - 500000000
		},
		{
			state: &ethpb.BeaconState{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
				},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance, params.BeaconConfig().MaxEffectiveBalance},
				Slashings: []uint64{0, 2 * 1e9},
			},
			// penalty    = validator balance / increment * (3*total_penalties) / total_balance * increment
			// 1000000000 = (32 * 1e9)        / (1 * 1e9) * (1*2e9)             / (64*1e9)      * (1 * 1e9)
			want: uint64(31000000000), // 32 * 1e9 - 1000000000
		},
		{
			state: &ethpb.BeaconState{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance - params.BeaconConfig().EffectiveBalanceIncrement},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance - params.BeaconConfig().EffectiveBalanceIncrement}},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance - params.BeaconConfig().EffectiveBalanceIncrement, params.BeaconConfig().MaxEffectiveBalance - params.BeaconConfig().EffectiveBalanceIncrement},
				Slashings: []uint64{0, 1e9},
			},
			// penalty    = validator balance           / increment * (3*total_penalties) / total_balance        * increment
			// 2000000000 = (32  * 1e9 - 1*1e9)         / (1 * 1e9) * (2*1e9)             / (31*1e9)             * (1 * 1e9)
			want: uint64(30000000000), // 32 * 1e9 - 2000000000
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			original := proto.Clone(tt.state)
			s, err := state_native.InitializeFromProtoPhase0(tt.state)
			require.NoError(t, err)
			helpers.ClearCache()
			require.NoError(t, epoch.ProcessSlashings(s))
			assert.Equal(t, tt.want, s.Balances()[0], "ProcessSlashings({%v}) = newState; newState.Balances[0] = %d", original, s.Balances()[0])
		})
	}
}

func TestProcessFinalUpdates_CanProcess(t *testing.T) {
	s := buildState(t, params.BeaconConfig().SlotsPerHistoricalRoot-1, uint64(params.BeaconConfig().SlotsPerEpoch))
	ce := time.CurrentEpoch(s)
	ne := ce + 1
	require.NoError(t, s.SetEth1DataVotes([]*ethpb.Eth1Data{}))
	balances := s.Balances()
	balances[0] = 31.75 * 1e9
	balances[1] = 31.74 * 1e9
	require.NoError(t, s.SetBalances(balances))

	slashings := s.Slashings()
	slashings[ce] = 0
	require.NoError(t, s.SetSlashings(slashings))
	mixes := s.RandaoMixes()
	mixes[ce] = []byte{'A'}
	require.NoError(t, s.SetRandaoMixes(mixes))
	newS, err := epoch.ProcessFinalUpdates(s)
	require.NoError(t, err)

	// Verify effective balance is correctly updated.
	assert.Equal(t, params.BeaconConfig().MaxEffectiveBalance, newS.Validators()[0].EffectiveBalance, "Effective balance incorrectly updated")
	assert.Equal(t, uint64(31*1e9), newS.Validators()[1].EffectiveBalance, "Effective balance incorrectly updated")

	// Verify slashed balances correctly updated.
	assert.Equal(t, newS.Slashings()[ce], newS.Slashings()[ne], "Unexpected slashed balance")

	// Verify randao is correctly updated in the right position.
	mix, err := newS.RandaoMixAtIndex(uint64(ne))
	assert.NoError(t, err)
	assert.DeepNotEqual(t, params.BeaconConfig().ZeroHash[:], mix, "latest RANDAO still zero hashes")

	// Verify historical root accumulator was appended.
	roots := newS.HistoricalRoots()
	assert.Equal(t, 1, len(roots), "Unexpected slashed balance")
	currAtt, err := newS.CurrentEpochAttestations()
	require.NoError(t, err)
	assert.NotNil(t, currAtt, "Nil value stored in current epoch attestations instead of empty slice")
}

func TestProcessRegistryUpdates_NoRotation(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot: 5 * params.BeaconConfig().SlotsPerEpoch,
		Validators: []*ethpb.Validator{
			{ExitEpoch: params.BeaconConfig().MaxSeedLookahead},
			{ExitEpoch: params.BeaconConfig().MaxSeedLookahead},
		},
		Balances: []uint64{
			params.BeaconConfig().MaxEffectiveBalance,
			params.BeaconConfig().MaxEffectiveBalance,
		},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		assert.Equal(t, params.BeaconConfig().MaxSeedLookahead, validator.ExitEpoch, "Could not update registry %d", i)
	}
}

func TestProcessRegistryUpdates_EligibleToActivate(t *testing.T) {
	finalizedEpoch := primitives.Epoch(4)
	base := &ethpb.BeaconState{
		Slot:                5 * params.BeaconConfig().SlotsPerEpoch,
		FinalizedCheckpoint: &ethpb.Checkpoint{Epoch: finalizedEpoch, Root: make([]byte, fieldparams.RootLength)},
	}
	limit := helpers.ValidatorActivationChurnLimit(0)
	for i := uint64(0); i < limit+10; i++ {
		base.Validators = append(base.Validators, &ethpb.Validator{
			ActivationEligibilityEpoch: finalizedEpoch,
			EffectiveBalance:           params.BeaconConfig().MaxEffectiveBalance,
			ActivationEpoch:            params.BeaconConfig().FarFutureEpoch,
		})
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	currentEpoch := time.CurrentEpoch(beaconState)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		if uint64(i) < limit && validator.ActivationEpoch != helpers.ActivationExitEpoch(currentEpoch) {
			t.Errorf("Could not update registry %d, validators failed to activate: wanted activation epoch %d, got %d",
				i, helpers.ActivationExitEpoch(currentEpoch), validator.ActivationEpoch)
		}
		if uint64(i) >= limit && validator.ActivationEpoch != params.BeaconConfig().FarFutureEpoch {
			t.Errorf("Could not update registry %d, validators should not have been activated, wanted activation epoch: %d, got %d",
				i, params.BeaconConfig().FarFutureEpoch, validator.ActivationEpoch)
		}
	}
}

func TestProcessRegistryUpdates_EligibleToActivate_Cancun(t *testing.T) {
	finalizedEpoch := primitives.Epoch(4)
	base := &ethpb.BeaconStateDeneb{
		Slot:                5 * params.BeaconConfig().SlotsPerEpoch,
		FinalizedCheckpoint: &ethpb.Checkpoint{Epoch: finalizedEpoch, Root: make([]byte, fieldparams.RootLength)},
	}
	cfg := params.BeaconConfig()
	cfg.MinPerEpochChurnLimit = 10
	cfg.ChurnLimitQuotient = 1
	params.OverrideBeaconConfig(cfg)

	for range uint64(10) {
		base.Validators = append(base.Validators, &ethpb.Validator{
			ActivationEligibilityEpoch: finalizedEpoch,
			EffectiveBalance:           params.BeaconConfig().MaxEffectiveBalance,
			ActivationEpoch:            params.BeaconConfig().FarFutureEpoch,
		})
	}
	beaconState, err := state_native.InitializeFromProtoDeneb(base)
	require.NoError(t, err)
	currentEpoch := time.CurrentEpoch(beaconState)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		// Note: In Deneb, only validators indices before `MaxPerEpochActivationChurnLimit` should be activated.
		if uint64(i) < params.BeaconConfig().MaxPerEpochActivationChurnLimit && validator.ActivationEpoch != helpers.ActivationExitEpoch(currentEpoch) {
			t.Errorf("Could not update registry %d, validators failed to activate: wanted activation epoch %d, got %d",
				i, helpers.ActivationExitEpoch(currentEpoch), validator.ActivationEpoch)
		}
		if uint64(i) >= params.BeaconConfig().MaxPerEpochActivationChurnLimit && validator.ActivationEpoch != params.BeaconConfig().FarFutureEpoch {
			t.Errorf("Could not update registry %d, validators should not have been activated, wanted activation epoch: %d, got %d",
				i, params.BeaconConfig().FarFutureEpoch, validator.ActivationEpoch)
		}
	}
}

func TestProcessRegistryUpdates_ActivationCompletes(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot: 5 * params.BeaconConfig().SlotsPerEpoch,
		Validators: []*ethpb.Validator{
			{ExitEpoch: params.BeaconConfig().MaxSeedLookahead,
				ActivationEpoch: 5 + params.BeaconConfig().MaxSeedLookahead + 1},
			{ExitEpoch: params.BeaconConfig().MaxSeedLookahead,
				ActivationEpoch: 5 + params.BeaconConfig().MaxSeedLookahead + 1},
		},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		assert.Equal(t, params.BeaconConfig().MaxSeedLookahead, validator.ExitEpoch, "Could not update registry %d, unexpected exit slot", i)
	}
}

func TestProcessRegistryUpdates_ValidatorsEjected(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot: 0,
		Validators: []*ethpb.Validator{
			{
				ExitEpoch:        params.BeaconConfig().FarFutureEpoch,
				EffectiveBalance: params.BeaconConfig().EjectionBalance - 1,
			},
			{
				ExitEpoch:        params.BeaconConfig().FarFutureEpoch,
				EffectiveBalance: params.BeaconConfig().EjectionBalance - 1,
			},
		},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		assert.Equal(t, params.BeaconConfig().MaxSeedLookahead+1, validator.ExitEpoch, "Could not update registry %d, unexpected exit slot", i)
	}
}

func TestProcessRegistryUpdates_CanExits(t *testing.T) {
	e := primitives.Epoch(5)
	exitEpoch := helpers.ActivationExitEpoch(e)
	minWithdrawalDelay := params.BeaconConfig().MinValidatorWithdrawabilityDelay
	base := &ethpb.BeaconState{
		Slot: params.BeaconConfig().SlotsPerEpoch.Mul(uint64(e)),
		Validators: []*ethpb.Validator{
			{
				ExitEpoch:         exitEpoch,
				WithdrawableEpoch: exitEpoch + minWithdrawalDelay},
			{
				ExitEpoch:         exitEpoch,
				WithdrawableEpoch: exitEpoch + minWithdrawalDelay},
		},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)
	for i, validator := range newState.Validators() {
		assert.Equal(t, exitEpoch, validator.ExitEpoch, "Could not update registry %d, unexpected exit slot", i)
	}
}

// TestProcessRegistryUpdates_HighInactivityScoreEjected exercises the
// LightChain inactivity-score ejection path. Validators whose score is at
// or above the ejection threshold are force-exited; validators just below
// the threshold are left alone. The threshold is derived at runtime from
// the chain's slot/epoch timing (see InactivityEjectionThreshold) so it
// corresponds to the same wall-clock downtime on every chain config.
func TestProcessRegistryUpdates_HighInactivityScoreEjected(t *testing.T) {
	threshold := epoch.InactivityEjectionThreshold()
	require.Equal(t, true, threshold > 0 && threshold < (1<<62),
		"derived threshold must be a sane finite value")

	// Four Altair validators, all active with full effective balance. Only
	// validator 1 sits exactly on the ejection threshold. Validator 3 keeps
	// the active set large enough that the quorum floor (2/3) permits one
	// ejection.
	base := &ethpb.BeaconStateAltair{
		Slot: 0,
		Validators: []*ethpb.Validator{
			{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
			{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
			{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
			{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
		},
		Balances: []uint64{
			params.BeaconConfig().MaxEffectiveBalance,
			params.BeaconConfig().MaxEffectiveBalance,
			params.BeaconConfig().MaxEffectiveBalance,
			params.BeaconConfig().MaxEffectiveBalance,
		},
		// threshold-1 must NOT trigger an exit — this pins the boundary so
		// future edits can't silently move it.
		InactivityScores:    []uint64{threshold - 1, threshold, 0, 0},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoAltair(base)
	require.NoError(t, err)

	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)

	farFuture := params.BeaconConfig().FarFutureEpoch
	vals := newState.Validators()
	require.Equal(t, 4, len(vals))

	assert.Equal(t, farFuture, vals[0].ExitEpoch,
		"validator 0 (score threshold-1) must not be ejected — boundary below threshold")
	assert.NotEqual(t, farFuture, vals[1].ExitEpoch,
		"validator 1 (score at threshold) must be ejected")
	assert.Equal(t, farFuture, vals[2].ExitEpoch,
		"validator 2 (score 0) must not be ejected")
	assert.Equal(t, farFuture, vals[3].ExitEpoch,
		"validator 3 (score 0) must not be ejected")
}

// TestProcessRegistryUpdates_InactivityEjectionQuorumFloor pins the
// LightChain quorum floor: when every validator's inactivity score crosses
// the threshold at once (the chain-wide-outage signature of the 2026-08-11
// mainnet incident), ejections must stop before the active set drops below
// 2/3 of its size. With 6 active validators, floor = 4, so exactly 2 (the
// lowest indices) are ejected and 4 must remain active.
func TestProcessRegistryUpdates_InactivityEjectionQuorumFloor(t *testing.T) {
	threshold := epoch.InactivityEjectionThreshold()
	n := 6
	validators := make([]*ethpb.Validator, n)
	balances := make([]uint64, n)
	scores := make([]uint64, n)
	for i := 0; i < n; i++ {
		validators[i] = &ethpb.Validator{
			ExitEpoch:        params.BeaconConfig().FarFutureEpoch,
			EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance,
		}
		balances[i] = params.BeaconConfig().MaxEffectiveBalance
		scores[i] = threshold + 100
	}
	base := &ethpb.BeaconStateAltair{
		Slot:                0,
		Validators:          validators,
		Balances:            balances,
		InactivityScores:    scores,
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoAltair(base)
	require.NoError(t, err)

	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)

	farFuture := params.BeaconConfig().FarFutureEpoch
	vals := newState.Validators()
	require.Equal(t, n, len(vals))

	ejected := 0
	for i, v := range vals {
		if v.ExitEpoch != farFuture {
			ejected++
			assert.Equal(t, true, i < 2,
				"only the lowest-index candidates may be ejected (deterministic cap order)")
		}
	}
	assert.Equal(t, 2, ejected,
		"exactly activeCount-floor validators may be ejected; the quorum floor must hold")
}

// TestProcessRegistryUpdates_InactivityEjectionFloorDoesNotCompound pins the
// fix for a gap in the original quorum floor: because the floor was
// measured against the *current* (already-shrinking) active count, a single
// outage spanning more than one epoch past the ejection threshold could
// still erode the active set well below 2/3 of its pre-outage size —
// 2/3, then 2/3 of that, then 2/3 of that again. Anchoring the floor to a
// snapshot from before the outage (inactivityEjectionFloorAnchorEpoch)
// fixes this without any new consensus state, by reconstructing historical
// active-set membership from each validator's existing activation/exit
// epoch fields.
//
// Scenario: 6 validators activated at genesis. Validators 0-1 were already
// ejected for inactivity in an earlier round (ExitEpoch = 200). We process
// a later epoch (300) where the 4 remaining validators are still inactive
// (the outage is ongoing) — well within the 24h anchor window, so the
// anchor epoch (300 - 225 = 75) predates the ejections. A live-count floor
// would compute floor = 2*4/3 = 2 and permit ejecting 2 more of the
// remaining 4; the anchored floor must compute floor = 2*6/3 = 4 against
// the pre-outage count of 6, and therefore permit zero further ejections
// (4 active, at the floor already).
func TestProcessRegistryUpdates_InactivityEjectionFloorDoesNotCompound(t *testing.T) {
	threshold := epoch.InactivityEjectionThreshold()
	n := 6
	validators := make([]*ethpb.Validator, n)
	balances := make([]uint64, n)
	scores := make([]uint64, n)
	for i := 0; i < n; i++ {
		exitEpoch := params.BeaconConfig().FarFutureEpoch
		score := threshold + 100
		if i < 2 {
			// Already ejected in an earlier round, well after the anchor
			// epoch (75) but before the current epoch (300).
			exitEpoch = 200
			score = 0
		}
		validators[i] = &ethpb.Validator{
			ExitEpoch:        exitEpoch,
			EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance,
		}
		balances[i] = params.BeaconConfig().MaxEffectiveBalance
		scores[i] = score
	}
	base := &ethpb.BeaconStateAltair{
		Slot:                primitives.Slot(300 * uint64(params.BeaconConfig().SlotsPerEpoch)),
		Validators:          validators,
		Balances:            balances,
		InactivityScores:    scores,
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoAltair(base)
	require.NoError(t, err)

	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err)

	farFuture := params.BeaconConfig().FarFutureEpoch
	vals := newState.Validators()
	require.Equal(t, n, len(vals))

	ejected := 0
	for _, v := range vals {
		if v.ExitEpoch != farFuture {
			ejected++
		}
	}
	assert.Equal(t, 2, ejected,
		"only the 2 validators already ejected in the earlier round should be exited; "+
			"the anchored floor must block ejecting any of the 4 still-active validators "+
			"even though a live-count floor would permit it")
}

// TestProcessRegistryUpdates_Phase0NoInactivityScores verifies that the
// LightChain patch does not break Phase 0 state processing. Phase 0 beacon
// states have no inactivity scores field, and calling st.InactivityScores()
// on them returns an error. The patch must version-gate that read so the
// Phase 0 spec-test suite continues to pass.
func TestProcessRegistryUpdates_Phase0NoInactivityScores(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot: 0,
		Validators: []*ethpb.Validator{
			{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
		},
		Balances:            []uint64{params.BeaconConfig().MaxEffectiveBalance},
		FinalizedCheckpoint: &ethpb.Checkpoint{Root: make([]byte, fieldparams.RootLength)},
	}
	beaconState, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)

	newState, err := epoch.ProcessRegistryUpdates(t.Context(), beaconState)
	require.NoError(t, err, "ProcessRegistryUpdates must not fail on Phase 0 states")

	vals := newState.Validators()
	require.Equal(t, 1, len(vals))
	assert.Equal(t, params.BeaconConfig().FarFutureEpoch, vals[0].ExitEpoch,
		"Phase 0 validator with full balance must not be ejected")
}

func buildState(t testing.TB, slot primitives.Slot, validatorCount uint64) state.BeaconState {
	validators := make([]*ethpb.Validator, validatorCount)
	for i := range validators {
		validators[i] = &ethpb.Validator{
			ExitEpoch:        params.BeaconConfig().FarFutureEpoch,
			EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance,
		}
	}
	validatorBalances := make([]uint64, len(validators))
	for i := range validatorBalances {
		validatorBalances[i] = params.BeaconConfig().MaxEffectiveBalance
	}
	latestActiveIndexRoots := make(
		[][]byte,
		params.BeaconConfig().EpochsPerHistoricalVector,
	)
	for i := range latestActiveIndexRoots {
		latestActiveIndexRoots[i] = params.BeaconConfig().ZeroHash[:]
	}
	latestRandaoMixes := make(
		[][]byte,
		params.BeaconConfig().EpochsPerHistoricalVector,
	)
	for i := range latestRandaoMixes {
		latestRandaoMixes[i] = params.BeaconConfig().ZeroHash[:]
	}
	s, err := util.NewBeaconState()
	require.NoError(t, err)
	if err := s.SetSlot(slot); err != nil {
		t.Error(err)
	}
	if err := s.SetBalances(validatorBalances); err != nil {
		t.Error(err)
	}
	if err := s.SetValidators(validators); err != nil {
		t.Error(err)
	}
	return s
}

func TestProcessSlashings_BadValue(t *testing.T) {
	base := &ethpb.BeaconState{
		Slot:       0,
		Validators: []*ethpb.Validator{{Slashed: true}},
		Balances:   []uint64{params.BeaconConfig().MaxEffectiveBalance},
		Slashings:  []uint64{math.MaxUint64, 1e9},
	}
	s, err := state_native.InitializeFromProtoPhase0(base)
	require.NoError(t, err)
	require.ErrorContains(t, "addition overflows", epoch.ProcessSlashings(s))
}

func TestProcessHistoricalDataUpdate(t *testing.T) {
	tests := []struct {
		name     string
		st       func() state.BeaconState
		verifier func(state.BeaconState)
	}{
		{
			name: "no change",
			st: func() state.BeaconState {
				st, _ := util.DeterministicGenesisState(t, 1)
				return st
			},
			verifier: func(st state.BeaconState) {
				roots := st.HistoricalRoots()
				require.Equal(t, 0, len(roots))
			},
		},
		{
			name: "before capella can process and get historical root",
			st: func() state.BeaconState {
				st, _ := util.DeterministicGenesisState(t, 1)
				st, err := transition.ProcessSlots(t.Context(), st, params.BeaconConfig().SlotsPerHistoricalRoot-1)
				require.NoError(t, err)
				return st
			},
			verifier: func(st state.BeaconState) {
				roots := st.HistoricalRoots()
				require.Equal(t, 1, len(roots))

				b := &ethpb.HistoricalBatch{
					BlockRoots: st.BlockRoots(),
					StateRoots: st.StateRoots(),
				}
				r, err := b.HashTreeRoot()
				require.NoError(t, err)
				require.DeepEqual(t, r[:], roots[0])

				_, err = st.HistoricalSummaries()
				require.ErrorContains(t, "HistoricalSummaries is not supported for phase0", err)
			},
		},
		{
			name: "after capella can process and get historical summary",
			st: func() state.BeaconState {
				st, _ := util.DeterministicGenesisStateCapella(t, 1)
				st, err := transition.ProcessSlots(t.Context(), st, params.BeaconConfig().SlotsPerHistoricalRoot-1)
				require.NoError(t, err)
				return st
			},
			verifier: func(st state.BeaconState) {
				summaries, err := st.HistoricalSummaries()
				require.NoError(t, err)
				require.Equal(t, 1, len(summaries))

				br, err := stateutil.ArraysRoot(st.BlockRoots(), fieldparams.BlockRootsLength)
				require.NoError(t, err)
				sr, err := stateutil.ArraysRoot(st.StateRoots(), fieldparams.StateRootsLength)
				require.NoError(t, err)
				b := &ethpb.HistoricalSummary{
					BlockSummaryRoot: br[:],
					StateSummaryRoot: sr[:],
				}
				require.DeepEqual(t, b, summaries[0])
				hrs := st.HistoricalRoots()
				require.DeepEqual(t, hrs, [][]byte{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := epoch.ProcessHistoricalDataUpdate(tt.st())
			require.NoError(t, err)
			tt.verifier(got)
		})
	}
}

func TestProcessSlashings_SlashedElectra(t *testing.T) {
	tests := []struct {
		state *ethpb.BeaconStateElectra
		want  uint64
	}{
		{
			state: &ethpb.BeaconStateElectra{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance}},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance, params.BeaconConfig().MaxEffectiveBalance},
				Slashings: []uint64{0, 1e9},
			},
			want: uint64(29000000000),
		},
		{
			state: &ethpb.BeaconStateElectra{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalance},
				},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance, params.BeaconConfig().MaxEffectiveBalance},
				Slashings: []uint64{0, 1e9},
			},
			want: uint64(30500000000),
		},
		{
			state: &ethpb.BeaconStateElectra{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalanceElectra},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalanceElectra},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalanceElectra},
				},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalance * 10, params.BeaconConfig().MaxEffectiveBalance * 20},
				Slashings: []uint64{0, 2 * 1e9},
			},
			want: uint64(317000001536),
		},
		{
			state: &ethpb.BeaconStateElectra{
				Validators: []*ethpb.Validator{
					{Slashed: true,
						WithdrawableEpoch: params.BeaconConfig().EpochsPerSlashingsVector / 2,
						EffectiveBalance:  params.BeaconConfig().MaxEffectiveBalanceElectra - params.BeaconConfig().EffectiveBalanceIncrement},
					{ExitEpoch: params.BeaconConfig().FarFutureEpoch, EffectiveBalance: params.BeaconConfig().MaxEffectiveBalanceElectra - params.BeaconConfig().EffectiveBalanceIncrement}},
				Balances:  []uint64{params.BeaconConfig().MaxEffectiveBalanceElectra - params.BeaconConfig().EffectiveBalanceIncrement, params.BeaconConfig().MaxEffectiveBalanceElectra - params.BeaconConfig().EffectiveBalanceIncrement},
				Slashings: []uint64{0, 1e9},
			},
			want: uint64(2044000000727),
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			original := proto.Clone(tt.state)
			s, err := state_native.InitializeFromProtoElectra(tt.state)
			require.NoError(t, err)
			helpers.ClearCache()
			require.NoError(t, epoch.ProcessSlashings(s))
			assert.Equal(t, tt.want, s.Balances()[0], "ProcessSlashings({%v}); s.Balances[0] = %d", original, s.Balances()[0])
		})
	}
}
