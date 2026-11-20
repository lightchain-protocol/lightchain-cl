package transition_test

// LightChain: release-gate tests. They run the state transition with the real
// mainnet chain config (testdata/lightchain-mainnet-config.yml) and a
// mainnet-shaped validator set: 69 validators at 5e14 Gwei effective balance
// on an Electra state.
//
// Why this file exists: mainnet halted on 2026-11-19 because attestationDelta
// multiplied the 5e14 Gwei effective balance by the inactivity score in 64
// bits and returned an error once the score passed 36893. Every upstream test
// uses 32e9 Gwei balances, so nothing in the suite ran the epoch transition
// with the numbers this chain has. These tests do, through
// transition.ProcessSlots, the same entry point a node uses for empty slots.
//
// Before commit b809bda52 (the 128-bit inactivity penalty) the cases of
// TestLightchainMainnetConfig_InactivityScorePastOverflow and the 46-of-69 and
// 45-of-69 cases of TestLightchainMainnetConfig_JustificationAndFinalization
// (the ones with validators that do not attest) fail with "multiplication
// overflows".

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/altair"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/epoch/precompute"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/transition"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/testing/util"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

const (
	// LightChain: copy of redswitches-deploy/networks/mainnet/config.yml; see the header of that file.
	lcMainnetConfigPath = "testdata/lightchain-mainnet-config.yml"

	// LightChain: the size of the mainnet validator set and the stake of each validator.
	lcValidatorCount   = 69
	lcEffectiveBalance = uint64(500_000_000_000_000)

	// LightChain: the tests start five epochs before a sync-committee period boundary (752128 = 2938 * 256, which is
	// also an eth1 voting period boundary), close to the epoch of the 2026-11-19 halt (752065), so that a run of a
	// few epochs rotates the sync committee.
	lcStartEpoch              = primitives.Epoch(752123)
	lcSyncCommitteePeriodEdge = primitives.Epoch(752128)

	// LightChain: 9229 epochs is how far finality was behind when mainnet halted.
	lcEpochsWithoutFinality = primitives.Epoch(9229)

	// LightChain: 5e14 * 36893 is the last product that fits in 64 bits; 5e14 * 36894 does not.
	lcLastScoreThatFits = uint64(36893)

	// LightChain: source, target and head flags all set, the participation byte of a validator that attested
	// correctly and in time.
	lcFullParticipation = byte(0b111)
)

// LightChain: lcUseMainnetConfig loads the real mainnet chain config and makes it the active config for the test.
// It fails the test if the file has a key this build does not know, because the loader only logs that: a build that
// lost the fork-specific keys (the slot-time fork) would otherwise run these tests with a config mainnet does not
// have.
func lcUseMainnetConfig(t *testing.T) {
	t.Helper()
	params.SetupTestConfigCleanup(t)
	hook := logTest.NewGlobal()
	// A copy of the preset is passed in: with nil the loader writes into the shared mainnet preset itself.
	cfg, err := params.UnmarshalConfigFile(lcMainnetConfigPath, params.MainnetConfig().Copy())
	require.NoError(t, err)
	require.LogsDoNotContain(t, hook, "issues parsing the config")
	require.LogsDoNotContain(t, hook, "Failed to decode hex string")
	params.OverrideBeaconConfig(cfg)
	helpers.ClearCache()
	t.Cleanup(helpers.ClearCache)
	// The skip-slot cache is keyed by slot and latest block header, which every state built here shares. It only
	// memoizes ProcessSlots, so turning it off changes no result; it keeps one test's states out of the next.
	transition.SkipSlotCache.Disable()
	t.Cleanup(transition.SkipSlotCache.Enable)

	// The values the tests below depend on. If one of these fails, the config was not loaded or the testdata copy
	// no longer describes mainnet.
	c := params.BeaconConfig()
	require.Equal(t, "lightchain-mainnet", c.ConfigName)
	require.Equal(t, lcEffectiveBalance, c.MaxEffectiveBalance)
	require.Equal(t, lcEffectiveBalance, c.MinActivationBalance)
	require.Equal(t, lcEffectiveBalance, c.MaxEffectiveBalanceElectra)
	require.Equal(t, uint64(0), c.BaseRewardFactor)
	require.Equal(t, primitives.Slot(6), c.SlotsPerEpoch)
	require.Equal(t, primitives.Epoch(322000), c.ElectraForkEpoch)
	require.Equal(t, uint64(2017074), c.SlotTimeForkOneSlot)
	require.Equal(t, true, lcStartEpoch > c.ElectraForkEpoch, "the tests must run after the Electra fork epoch")
	start, err := slots.EpochStart(lcStartEpoch)
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, slots.SlotDurationAt(start), "the slot-time fork must be active")
	require.Equal(t, primitives.Epoch(0), lcSyncCommitteePeriodEdge%c.EpochsPerSyncCommitteePeriod)
	// The arithmetic the overflow tests rest on.
	require.Equal(t, true, lcEffectiveBalance <= ^uint64(0)/lcLastScoreThatFits)
	require.Equal(t, true, lcEffectiveBalance > ^uint64(0)/(lcLastScoreThatFits+1))
}

// LightChain: lcMainnetShapedState returns an Electra state at the first slot of lcStartEpoch with 69 active
// validators at 5e14 Gwei, every validator holding the given inactivity score, and all three checkpoints at
// finalizedEpoch. Call lcUseMainnetConfig first.
func lcMainnetShapedState(t *testing.T, inactivityScore uint64, finalizedEpoch primitives.Epoch) state.BeaconState {
	t.Helper()
	st, _ := util.DeterministicGenesisStateElectra(t, lcValidatorCount)
	require.Equal(t, version.Electra, st.Version())
	start, err := slots.EpochStart(lcStartEpoch)
	require.NoError(t, err)
	require.NoError(t, st.SetSlot(start))

	scores := make([]uint64, lcValidatorCount)
	for i := range scores {
		scores[i] = inactivityScore
	}
	require.NoError(t, st.SetInactivityScores(scores))

	cp := &ethpb.Checkpoint{Epoch: finalizedEpoch, Root: make([]byte, 32)}
	require.NoError(t, st.SetFinalizedCheckpoint(cp))
	require.NoError(t, st.SetCurrentJustifiedCheckpoint(cp))
	require.NoError(t, st.SetPreviousJustifiedCheckpoint(cp))
	require.NoError(t, st.SetJustificationBits([]byte{0}))

	require.Equal(t, lcValidatorCount, st.NumValidators())
	for i, v := range st.Validators() {
		require.Equal(t, lcEffectiveBalance, v.EffectiveBalance, "validator %d", i)
	}
	lcRequireBalancesUnchanged(t, st)
	lcRequireAllActive(t, st)
	return st
}

// LightChain: lcParticipation returns a participation list in which the first attesting validators have all three
// flags set and the rest have none.
func lcParticipation(attesting int) []byte {
	p := make([]byte, lcValidatorCount)
	for i := 0; i < attesting; i++ {
		p[i] = lcFullParticipation
	}
	return p
}

// LightChain: lcNextEpoch runs transition.ProcessSlots to the first slot of the next epoch, which runs one Electra
// epoch transition, and returns the error instead of failing so that each test can say what the error means.
func lcNextEpoch(t *testing.T, st state.BeaconState) (state.BeaconState, error) {
	t.Helper()
	next, err := slots.EpochStart(slots.ToEpoch(st.Slot()) + 1)
	require.NoError(t, err)
	return transition.ProcessSlots(t.Context(), st, next)
}

// LightChain: lcRequireInactivityPenalties takes the state as it was before an epoch transition and runs, on a copy,
// the steps electra.ProcessEpoch runs before it applies rewards and penalties (participation, justification,
// inactivity scores), then asks AttestationsDelta for the penalties of that transition. It requires every validator that did not attest to have a
// non-zero inactivity penalty equal to effective balance * score / (bias * quotient), computed here without
// overflow. This is the penalty the fork computes and then does not subtract: the balance checks after the
// transition show that it is not applied. Before commit b809bda52 AttestationsDelta returned an error here.
func lcRequireInactivityPenalties(t *testing.T, st state.BeaconState, attesting int) {
	t.Helper()
	ctx := t.Context()
	// The epoch transition runs at the last slot of the epoch, after that slot's ProcessSlot; bring the copy there.
	last, err := slots.EpochEnd(slots.ToEpoch(st.Slot()))
	require.NoError(t, err)
	cp, err := transition.ProcessSlots(ctx, st.Copy(), last)
	require.NoError(t, err)
	cp, err = transition.ProcessSlot(ctx, cp)
	require.NoError(t, err)
	vp, bp, err := altair.InitializePrecomputeValidators(ctx, cp)
	require.NoError(t, err)
	vp, bp, err = altair.ProcessEpochParticipation(ctx, cp, bp, vp)
	require.NoError(t, err)
	cp, err = precompute.ProcessJustificationAndFinalizationPreCompute(cp, bp)
	require.NoError(t, err)
	cp, vp, err = altair.ProcessInactivityScores(ctx, cp, vp)
	require.NoError(t, err)
	deltas, err := altair.AttestationsDelta(cp, bp, vp)
	require.NoError(t, err, "computing the inactivity penalty failed at slot %d: this is the 2026-11-19 mainnet halt", st.Slot())

	quotient, err := cp.InactivityPenaltyQuotient()
	require.NoError(t, err)
	denominator := new(big.Int).SetUint64(params.BeaconConfig().InactivityScoreBias * quotient)
	require.Equal(t, lcValidatorCount, len(deltas))
	for j := attesting; j < lcValidatorCount; j++ {
		want := new(big.Int).Mul(new(big.Int).SetUint64(lcEffectiveBalance), new(big.Int).SetUint64(vp[j].InactivityScore))
		want.Quo(want, denominator)
		require.Equal(t, true, want.IsUint64(), "test values must keep the penalty within 64 bits")
		require.Equal(t, want.Uint64(), deltas[j].InactivityPenalty, "inactivity penalty of validator %d with score %d", j, vp[j].InactivityScore)
		require.Equal(t, true, deltas[j].InactivityPenalty > 0, "inactivity penalty of validator %d", j)
	}
}

// LightChain: every balance must still be 5e14 Gwei. With BASE_REWARD_FACTOR 0 there are no rewards and no flag
// penalties, and this fork does not subtract the inactivity penalty, so the epoch transition must not move any
// balance.
func lcRequireBalancesUnchanged(t *testing.T, st state.BeaconState) {
	t.Helper()
	balances := st.Balances()
	require.Equal(t, lcValidatorCount, len(balances))
	for i, b := range balances {
		require.Equal(t, lcEffectiveBalance, b, "balance of validator %d at slot %d", i, st.Slot())
	}
}

// LightChain: no validator may have been exited or ejected by the epoch transition. The Electra registry update
// has no inactivity ejection (the fork's ejection with its quorum floor is in core/epoch, which Electra does not
// call), so a long period without finality must leave all 69 active. If that changes, this check fails on purpose:
// a validator set that shrinks without finality is how the 2026-08-11 halt became permanent.
func lcRequireAllActive(t *testing.T, st state.BeaconState) {
	t.Helper()
	count, err := helpers.ActiveValidatorCount(t.Context(), st, slots.ToEpoch(st.Slot()))
	require.NoError(t, err)
	require.Equal(t, uint64(lcValidatorCount), count, "active validators at slot %d", st.Slot())
	far := params.BeaconConfig().FarFutureEpoch
	for i, v := range st.Validators() {
		require.Equal(t, far, v.ExitEpoch, "exit epoch of validator %d at slot %d", i, st.Slot())
		require.Equal(t, lcEffectiveBalance, v.EffectiveBalance, "effective balance of validator %d at slot %d", i, st.Slot())
	}
}

// LightChain: (a) empty epochs with the mainnet config on a healthy chain (finality two epochs behind, as it is
// when every validator attests). Eight epochs, crossing a sync-committee period boundary. Nobody attests in the
// test, so the chain also enters the inactivity leak on the way. Every epoch transition must succeed.
func TestLightchainMainnetConfig_EmptyEpochsAcrossSyncCommitteePeriod(t *testing.T) {
	lcUseMainnetConfig(t)
	st := lcMainnetShapedState(t, 0, lcStartEpoch-2)

	nextBefore, err := st.NextSyncCommittee()
	require.NoError(t, err)

	const epochs = 8
	for i := 0; i < epochs; i++ {
		epoch := slots.ToEpoch(st.Slot())
		st, err = lcNextEpoch(t, st)
		require.NoError(t, err, "epoch transition out of epoch %d failed", epoch)
		require.Equal(t, epoch+1, slots.ToEpoch(st.Slot()))
		lcRequireBalancesUnchanged(t, st)
		lcRequireAllActive(t, st)
	}
	require.Equal(t, true, slots.ToEpoch(st.Slot()) > lcSyncCommitteePeriodEdge, "the run must cross the period boundary")

	// The rotation happened: the committee that was "next" is now "current", and a full new one was drawn from
	// the 69 validators.
	current, err := st.CurrentSyncCommittee()
	require.NoError(t, err)
	require.DeepEqual(t, nextBefore.Pubkeys, current.Pubkeys)
	next, err := st.NextSyncCommittee()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().SyncCommitteeSize, uint64(len(next.Pubkeys)))

	// Nobody attested, so nothing was justified or finalized.
	require.Equal(t, lcStartEpoch-2, st.FinalizedCheckpointEpoch())
}

// LightChain: (b) the 2026-11-19 halt. Finality is 9229 epochs behind, nobody attests, and every validator's
// inactivity score is at or past the point where score * 5e14 no longer fits in 64 bits. Before commit b809bda52
// attestationDelta returned "multiplication overflows" there and every epoch transition failed. Each transition
// must succeed, must compute a non-zero inactivity penalty, must leave every balance alone (this fork does not
// subtract the inactivity penalty) and must not exit anybody.
func TestLightchainMainnetConfig_InactivityScorePastOverflow(t *testing.T) {
	bias := uint64(4) // INACTIVITY_SCORE_BIAS, checked against the loaded config below.
	tests := []struct {
		name  string
		score uint64
	}{
		// The score rises by the bias in each epoch transition before the penalty is computed, so this case
		// crosses the 64-bit limit in its second epoch, the way mainnet did.
		{name: "crosses the 64-bit limit during the run", score: lcLastScoreThatFits - 1 - bias},
		{name: "first score whose product overflows", score: lcLastScoreThatFits + 1},
		{name: "score at the mainnet halt", score: 36896},
		{name: "one year without finality", score: 4 * 2_628_000},
		{name: "2^32", score: 1 << 32},
		{name: "2^40", score: 1 << 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcUseMainnetConfig(t)
			require.Equal(t, bias, params.BeaconConfig().InactivityScoreBias)
			finalized := lcStartEpoch - lcEpochsWithoutFinality
			st := lcMainnetShapedState(t, tt.score, finalized)

			var err error
			const epochs = 7
			for i := 0; i < epochs; i++ {
				epoch := slots.ToEpoch(st.Slot())
				pre := st.Copy()
				st, err = lcNextEpoch(t, st)
				require.NoError(t, err, "epoch transition out of epoch %d failed with inactivity score %d: "+
					"this is the 2026-11-19 mainnet halt", epoch, tt.score+uint64(i+1)*bias)
				// The penalty this transition computed was not zero, and the balances show it was not applied.
				lcRequireInactivityPenalties(t, pre, 0)
				lcRequireBalancesUnchanged(t, st)
				lcRequireAllActive(t, st)

				// The score keeps counting: nobody attested, so it rises by the bias every epoch.
				scores, err := st.InactivityScores()
				require.NoError(t, err)
				for j, s := range scores {
					require.Equal(t, tt.score+uint64(i+1)*bias, s, "inactivity score of validator %d", j)
				}
			}
			require.Equal(t, true, slots.ToEpoch(st.Slot()) > lcSyncCommitteePeriodEdge, "the run must cross the period boundary")
			require.Equal(t, finalized, st.FinalizedCheckpointEpoch())
			scores, err := st.InactivityScores()
			require.NoError(t, err)
			require.Equal(t, true, scores[0] > lcLastScoreThatFits, "the run must end past the 64-bit limit")
		})
	}
}

// LightChain: (c) justification and finalization with the mainnet config, starting from the state of the halt
// (finality 9229 epochs behind, inactivity scores past the 64-bit limit). Attestations are represented by the
// participation flags they would have set. With all 69 attesting, and with 46 (exactly two thirds of the stake),
// the first transition must justify, the second must finalize, and finality must then stay two epochs behind the
// head. With 45 nothing may be justified, and the epoch transition must still succeed for the 24 validators whose
// score keeps rising past the 64-bit limit.
func TestLightchainMainnetConfig_JustificationAndFinalization(t *testing.T) {
	const startScore = uint64(36896)
	tests := []struct {
		attesting int
		finalizes bool
	}{
		{attesting: 69, finalizes: true},
		{attesting: 46, finalizes: true},
		{attesting: 45, finalizes: false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d of 69 attest", tt.attesting), func(t *testing.T) {
			lcUseMainnetConfig(t)
			cfg := params.BeaconConfig()
			stalled := lcStartEpoch - lcEpochsWithoutFinality
			st := lcMainnetShapedState(t, startScore, stalled)
			// The attesters also attested in the epoch before the start epoch.
			require.NoError(t, st.SetPreviousParticipationBits(lcParticipation(tt.attesting)))

			want := make([]uint64, lcValidatorCount)
			for j := range want {
				want[j] = startScore
			}

			var err error
			const epochs = 7
			for i := 0; i < epochs; i++ {
				epoch := slots.ToEpoch(st.Slot())
				// The epoch transition rotates the participation lists, so the attestations of the epoch that
				// is about to end are set before every transition.
				require.NoError(t, st.SetCurrentParticipationBits(lcParticipation(tt.attesting)))
				pre := st.Copy()
				st, err = lcNextEpoch(t, st)
				require.NoError(t, err, "epoch transition out of epoch %d failed", epoch)
				lcRequireInactivityPenalties(t, pre, tt.attesting)
				lcRequireBalancesUnchanged(t, st)
				lcRequireAllActive(t, st)

				current := slots.ToEpoch(st.Slot())
				switch {
				case !tt.finalizes:
					require.Equal(t, stalled, st.CurrentJustifiedCheckpoint().Epoch, "nothing may be justified with less than two thirds")
					require.Equal(t, stalled, st.FinalizedCheckpointEpoch(), "nothing may be finalized with less than two thirds")
				case i == 0:
					// The first transition justifies the epoch that just ended; nothing is finalized yet.
					require.Equal(t, current-1, st.CurrentJustifiedCheckpoint().Epoch)
					require.Equal(t, stalled, st.FinalizedCheckpointEpoch())
				default:
					require.Equal(t, current-1, st.CurrentJustifiedCheckpoint().Epoch, "justified epoch after %d transitions", i+1)
					require.Equal(t, current-2, st.FinalizedCheckpointEpoch(), "finalized epoch after %d transitions", i+1)
				}

				// The inactivity scores, by the rule of process_inactivity_updates. Justification runs first in
				// the epoch transition, so the leak ends in the transition that restores finality (the second),
				// and from then on every score also recovers by INACTIVITY_SCORE_RECOVERY_RATE.
				inLeak := !tt.finalizes || i == 0
				for j := range want {
					if j < tt.attesting {
						want[j] -= min(1, want[j])
					} else {
						want[j] += cfg.InactivityScoreBias
					}
					if !inLeak {
						want[j] -= min(cfg.InactivityScoreRecoveryRate, want[j])
					}
				}
				scores, err := st.InactivityScores()
				require.NoError(t, err)
				require.DeepEqual(t, want, scores, "inactivity scores after %d transitions", i+1)
			}
			require.Equal(t, true, slots.ToEpoch(st.Slot()) > lcSyncCommitteePeriodEdge, "the run must cross the period boundary")
		})
	}
}
