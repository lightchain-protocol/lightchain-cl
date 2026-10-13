package altair_test

import (
	"testing"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/altair"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/testing/util"
)

// balanceAfterMissedEpoch is the balance the tests below expect: a validator with 32 ETH, one of 2048, after an epoch
// of inactivity leak in which it attested to nothing. This fork does not take the inactivity penalty from the balance
// (see ProcessRewardsAndPenaltiesPrecompute), so the validator only loses the source and the target penalty:
//
//	base reward    = 32 * (1e9 * 64 / isqrt(2048 * 32e9)) = 32 * (64e9 / 8095430) = 32 * 7905 = 252960
//	source penalty = 252960 * 14 / 64 = 55335
//	target penalty = 252960 * 26 / 64 = 102765
//	balance        = 32e9 - 55335 - 102765 = 31999841900
//
// Upstream also takes 32e9 * 4 / (4 * quotient) for an inactivity score of 4, which is 635 in Altair (quotient 3 * 2^24,
// balance 31999841265) and 1907 in Bellatrix (quotient 2^24, balance 31999839993). With that penalty gone the two
// forks agree.
const balanceAfterMissedEpoch = uint64(31999841900)

func TestProcessEpoch_CanProcess(t *testing.T) {
	st, _ := util.DeterministicGenesisStateAltair(t, params.BeaconConfig().MaxValidatorsPerCommittee)
	require.NoError(t, st.SetSlot(10*params.BeaconConfig().SlotsPerEpoch))
	err := altair.ProcessEpoch(t.Context(), st)
	require.NoError(t, err)
	require.Equal(t, uint64(0), st.Slashings()[2], "Unexpected slashed balance")

	b := st.Balances()
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(b)))
	require.Equal(t, balanceAfterMissedEpoch, b[0])

	s, err := st.InactivityScores()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(s)))

	p, err := st.PreviousEpochParticipation()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(p)))

	p, err = st.CurrentEpochParticipation()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(p)))

	sc, err := st.CurrentSyncCommittee()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().SyncCommitteeSize, uint64(len(sc.Pubkeys)))

	sc, err = st.NextSyncCommittee()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().SyncCommitteeSize, uint64(len(sc.Pubkeys)))
}

func TestProcessEpoch_CanProcessBellatrix(t *testing.T) {
	st, _ := util.DeterministicGenesisStateBellatrix(t, params.BeaconConfig().MaxValidatorsPerCommittee)
	require.NoError(t, st.SetSlot(10*params.BeaconConfig().SlotsPerEpoch))
	err := altair.ProcessEpoch(t.Context(), st)
	require.NoError(t, err)
	require.Equal(t, uint64(0), st.Slashings()[2], "Unexpected slashed balance")

	b := st.Balances()
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(b)))
	require.Equal(t, balanceAfterMissedEpoch, b[0])

	s, err := st.InactivityScores()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(s)))

	p, err := st.PreviousEpochParticipation()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(p)))

	p, err = st.CurrentEpochParticipation()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().MaxValidatorsPerCommittee, uint64(len(p)))

	sc, err := st.CurrentSyncCommittee()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().SyncCommitteeSize, uint64(len(sc.Pubkeys)))

	sc, err = st.NextSyncCommittee()
	require.NoError(t, err)
	require.Equal(t, params.BeaconConfig().SyncCommitteeSize, uint64(len(sc.Pubkeys)))
}
