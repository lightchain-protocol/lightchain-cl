// Package epoch contains epoch processing libraries according to spec, able to
// process new balance for validators, justify and finalize new
// check points, and shuffle validators to different slots and
// shards.
package epoch

import (
	"context"
	"fmt"
	"sort"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/time"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/validators"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state/stateutil"
	fieldparams "github.com/OffchainLabs/prysm/v7/config/fieldparams"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/math"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	"github.com/pkg/errors"
)

// inactivityEjectionMinDowntimeSeconds is the minimum wall-clock duration of
// continuous inactivity-leak participation failure before a validator becomes
// eligible for LightChain's forced exit (which replaces the inactivity
// penalty on this fork to preserve the fixed-supply invariant).
//
// The score threshold is derived from this duration at runtime via
// InactivityEjectionThreshold, using the active chain config's
// SECONDS_PER_SLOT, SLOTS_PER_EPOCH, and INACTIVITY_SCORE_BIAS. The original
// implementation hardcoded 256, a value computed from Ethereum-mainnet epoch
// timing (~6.8h of leak) — but on LightChain's 2s-slot / 6-slot-epoch chains
// 256 was reached after ~13 minutes of non-finality. On 2026-08-11 a
// ~90-minute mainnet halt force-exited the entire validator set, permanently
// halting the chain (empty active set → no proposer computable); recovery
// required a coordinated replay from the last pre-ejection finalized
// checkpoint. Deriving from wall-clock time makes the threshold mean the
// same thing on every chain this fork runs.
const inactivityEjectionMinDowntimeSeconds uint64 = 6 * 60 * 60 // 6 hours

// InactivityEjectionThreshold returns the inactivity score at or above which
// a validator becomes a candidate for forced exit, derived from the active
// beacon config so the threshold corresponds to
// inactivityEjectionMinDowntimeSeconds of continuous leak on this chain.
// Exported for tests and operator tooling.
func InactivityEjectionThreshold() uint64 {
	cfg := params.BeaconConfig()
	epochSeconds := cfg.SecondsPerSlot * uint64(cfg.SlotsPerEpoch)
	if epochSeconds == 0 || cfg.InactivityScoreBias == 0 {
		// Misconfigured chain params: fail safe by never ejecting.
		return ^uint64(0)
	}
	leakEpochs := inactivityEjectionMinDowntimeSeconds / epochSeconds
	if leakEpochs == 0 {
		leakEpochs = 1
	}
	return leakEpochs * cfg.InactivityScoreBias
}

// ProcessRegistryUpdates rotates validators in and out of active pool.
// the amount to rotate is determined churn limit.
//
// Spec pseudocode definition:
//
//	def process_registry_updates(state: BeaconState) -> None:
//	 # Process activation eligibility and ejections
//	 for index, validator in enumerate(state.validators):
//	     if is_eligible_for_activation_queue(validator):
//	         validator.activation_eligibility_epoch = get_current_epoch(state) + 1
//
//	     if is_active_validator(validator, get_current_epoch(state)) and validator.effective_balance <= EJECTION_BALANCE:
//	         initiate_validator_exit(state, ValidatorIndex(index))
//
//	 # Queue validators eligible for activation and not yet dequeued for activation
//	 activation_queue = sorted([
//	     index for index, validator in enumerate(state.validators)
//	     if is_eligible_for_activation(state, validator)
//	     # Order by the sequence of activation_eligibility_epoch setting and then index
//	 ], key=lambda index: (state.validators[index].activation_eligibility_epoch, index))
//	 # Dequeued validators for activation up to churn limit
//	 for index in activation_queue[:get_validator_churn_limit(state)]:
//	     validator = state.validators[index]
//	     validator.activation_epoch = compute_activation_exit_epoch(get_current_epoch(state))
func ProcessRegistryUpdates(ctx context.Context, st state.BeaconState) (state.BeaconState, error) {
	currentEpoch := time.CurrentEpoch(st)
	var err error
	ejectionBal := params.BeaconConfig().EjectionBalance

	// LightChain: read inactivity scores once before the validator loop so we
	// can also eject validators with persistently high scores (in addition to
	// those below EjectionBalance).
	//
	// InactivityScores is an Altair+ field; on Phase 0 states the getter
	// returns errNotSupported, so we skip the read entirely there. An empty
	// (length 0) scores slice is treated as "no inactivity data available"
	// and falls through to balance-only ejection — this handles initialization
	// paths where the state has not yet populated scores. A non-empty slice
	// whose length disagrees with NumValidators is a real state-consistency
	// bug and is rejected.
	var inactivityScores []uint64
	if st.Version() >= version.Altair {
		inactivityScores, err = st.InactivityScores()
		if err != nil {
			return nil, errors.Wrap(err, "could not read inactivity scores")
		}
		if len(inactivityScores) > 0 && len(inactivityScores) != st.NumValidators() {
			return nil, errors.Errorf(
				"inactivity scores length %d does not match validator count %d",
				len(inactivityScores), st.NumValidators(),
			)
		}
	}

	// To avoid copying the state validator set via st.Validators(), we will perform a read only pass
	// over the validator set while collecting validator indices where the validator copy is actually
	// necessary, then we will process these operations.
	eligibleForActivationQ := make([]primitives.ValidatorIndex, 0)
	eligibleForActivation := make([]primitives.ValidatorIndex, 0)
	eligibleForEjection := make([]primitives.ValidatorIndex, 0)
	// LightChain: inactivity-based ejection candidates are collected separately
	// from spec (balance) ejections so a quorum floor can cap them below.
	inactivityCandidates := make([]primitives.ValidatorIndex, 0)
	activeCount := 0
	inactivityThreshold := InactivityEjectionThreshold()

	if err := st.ReadFromEveryValidator(func(idx int, val state.ReadOnlyValidator) error {
		// Collect validators eligible to enter the activation queue.
		if helpers.IsEligibleForActivationQueue(val, currentEpoch) {
			eligibleForActivationQ = append(eligibleForActivationQ, primitives.ValidatorIndex(idx))
		}

		// Collect validators to eject.
		isActive := helpers.IsActiveValidatorUsingTrie(val, currentEpoch)
		if isActive {
			activeCount++
		}
		belowEjectionBalance := val.EffectiveBalance() <= ejectionBal
		// LightChain: also eject validators with persistent inactivity.
		// Empty inactivityScores (Phase 0, or states that have not yet
		// populated the field) skip this branch and behavior matches
		// upstream. The length invariant has already been checked above,
		// so idx is guaranteed in bounds whenever len > 0.
		highInactivity := len(inactivityScores) > 0 && inactivityScores[idx] >= inactivityThreshold
		if isActive && belowEjectionBalance {
			eligibleForEjection = append(eligibleForEjection, primitives.ValidatorIndex(idx))
		} else if isActive && highInactivity {
			inactivityCandidates = append(inactivityCandidates, primitives.ValidatorIndex(idx))
		}

		// Collect validators eligible for activation and not yet dequeued for activation.
		if helpers.IsEligibleForActivationUsingROVal(st, val) {
			eligibleForActivation = append(eligibleForActivation, primitives.ValidatorIndex(idx))
		}

		return nil
	}); err != nil {
		return st, fmt.Errorf("failed to read validators: %w", err)
	}

	// LightChain quorum floor: inactivity ejections may never shrink the
	// active set below 2/3 of its current size (minimum 1). During a
	// chain-wide outage every validator's score rises together; without this
	// cap a single long halt force-exits the entire set and permanently
	// bricks the chain (2026-08-11 mainnet incident). Candidates are capped
	// in ascending validator-index order, which is deterministic across
	// nodes; spec (balance) ejections are mandatory and count against the
	// remaining capacity but are never themselves capped.
	if len(inactivityCandidates) > 0 {
		floor := (2 * activeCount) / 3
		if floor < 1 {
			floor = 1
		}
		capacity := activeCount - len(eligibleForEjection) - floor
		if capacity < 0 {
			capacity = 0
		}
		if len(inactivityCandidates) > capacity {
			inactivityCandidates = inactivityCandidates[:capacity]
		}
		eligibleForEjection = append(eligibleForEjection, inactivityCandidates...)
	}

	// Process validators for activation eligibility.
	activationEligibilityEpoch := time.CurrentEpoch(st) + 1
	for _, idx := range eligibleForActivationQ {
		v, err := st.ValidatorAtIndex(idx)
		if err != nil {
			return nil, err
		}
		v.ActivationEligibilityEpoch = activationEligibilityEpoch
		if err := st.UpdateValidatorAtIndex(idx, v); err != nil {
			return nil, err
		}
	}

	// Process validators eligible for ejection.
	if len(eligibleForEjection) > 0 {
		// It is safe to compute exitInfo once for all ejections in the epoch, as the ExitInfo pointer is
		// updated within InitiateValidatorExit which is the only function that uses it.
		exitInfo := validators.ExitInformation(st)
		for _, idx := range eligibleForEjection {
			// Here is fine to do a quadratic loop since this should
			// barely happen
			st, err = validators.InitiateValidatorExit(ctx, st, idx, exitInfo)
			if err != nil && !errors.Is(err, validators.ErrValidatorAlreadyExited) {
				return nil, errors.Wrapf(err, "could not initiate exit for validator %d", idx)
			}
		}
	}

	// Queue validators eligible for activation and not yet dequeued for activation.
	sort.Sort(sortableIndices{indices: eligibleForActivation, state: st})

	// Only activate just enough validators according to the activation churn limit.
	limit := uint64(len(eligibleForActivation))
	activeValidatorCount, err := helpers.ActiveValidatorCount(ctx, st, currentEpoch)
	if err != nil {
		return nil, errors.Wrap(err, "could not get active validator count")
	}

	churnLimit := helpers.ValidatorActivationChurnLimit(activeValidatorCount)

	if st.Version() >= version.Deneb {
		churnLimit = helpers.ValidatorActivationChurnLimitDeneb(activeValidatorCount)
	}

	// Prevent churn limit cause index out of bound.
	if churnLimit < limit {
		limit = churnLimit
	}

	activationExitEpoch := helpers.ActivationExitEpoch(currentEpoch)
	for _, index := range eligibleForActivation[:limit] {
		validator, err := st.ValidatorAtIndex(index)
		if err != nil {
			return nil, err
		}
		validator.ActivationEpoch = activationExitEpoch
		if err := st.UpdateValidatorAtIndex(index, validator); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// ProcessSlashings processes the slashed validators during epoch processing. This is a state mutating method.
//
// Electra spec definition:
//
//	def process_slashings(state: BeaconState) -> None:
//	    epoch = get_current_epoch(state)
//	    total_balance = get_total_active_balance(state)
//	    adjusted_total_slashing_balance = min(
//	        sum(state.slashings) * PROPORTIONAL_SLASHING_MULTIPLIER_BELLATRIX,
//	        total_balance
//	    )
//	    increment = EFFECTIVE_BALANCE_INCREMENT  # Factored out from total balance to avoid uint64 overflow
//	    penalty_per_effective_balance_increment = adjusted_total_slashing_balance // (total_balance // increment)
//	    for index, validator in enumerate(state.validators):
//	        if validator.slashed and epoch + EPOCHS_PER_SLASHINGS_VECTOR // 2 == validator.withdrawable_epoch:
//	            effective_balance_increments = validator.effective_balance // increment
//	            # [Modified in Electra:EIP7251]
//	            penalty = penalty_per_effective_balance_increment * effective_balance_increments
//	            decrease_balance(state, ValidatorIndex(index), penalty)
//
// Bellatrix spec definition:
//
//	def process_slashings(state: BeaconState) -> None:
//	    epoch = get_current_epoch(state)
//	    total_balance = get_total_active_balance(state)
//	    adjusted_total_slashing_balance = min(
//	        sum(state.slashings) * PROPORTIONAL_SLASHING_MULTIPLIER_BELLATRIX,  # [Modified in Bellatrix]
//	        total_balance
//	    )
//	    for index, validator in enumerate(state.validators):
//	        if validator.slashed and epoch + EPOCHS_PER_SLASHINGS_VECTOR // 2 == validator.withdrawable_epoch:
//	            increment = EFFECTIVE_BALANCE_INCREMENT  # Factored out from penalty numerator to avoid uint64 overflow
//	            penalty_numerator = validator.effective_balance // increment * adjusted_total_slashing_balance
//	            penalty = penalty_numerator // total_balance * increment
//	            decrease_balance(state, ValidatorIndex(index), penalty)
//
// Altair spec definition:
//
//	def process_slashings(state: BeaconState) -> None:
//	    epoch = get_current_epoch(state)
//	    total_balance = get_total_active_balance(state)
//	    adjusted_total_slashing_balance = min(sum(state.slashings) * PROPORTIONAL_SLASHING_MULTIPLIER_ALTAIR, total_balance)
//	    for index, validator in enumerate(state.validators):
//	        if validator.slashed and epoch + EPOCHS_PER_SLASHINGS_VECTOR // 2 == validator.withdrawable_epoch:
//	            increment = EFFECTIVE_BALANCE_INCREMENT  # Factored out from penalty numerator to avoid uint64 overflow
//	            penalty_numerator = validator.effective_balance // increment * adjusted_total_slashing_balance
//	            penalty = penalty_numerator // total_balance * increment
//	            decrease_balance(state, ValidatorIndex(index), penalty)
//
// Phase0 spec definition:
//
//	def process_slashings(state: BeaconState) -> None:
//	    epoch = get_current_epoch(state)
//	    total_balance = get_total_active_balance(state)
//	    adjusted_total_slashing_balance = min(sum(state.slashings) * PROPORTIONAL_SLASHING_MULTIPLIER, total_balance)
//	    for index, validator in enumerate(state.validators):
//	        if validator.slashed and epoch + EPOCHS_PER_SLASHINGS_VECTOR // 2 == validator.withdrawable_epoch:
//	            increment = EFFECTIVE_BALANCE_INCREMENT  # Factored out from penalty numerator to avoid uint64 overflow
//	            penalty_numerator = validator.effective_balance // increment * adjusted_total_slashing_balance
//	            penalty = penalty_numerator // total_balance * increment
//	            decrease_balance(state, ValidatorIndex(index), penalty)
func ProcessSlashings(st state.BeaconState) error {
	slashingMultiplier, err := st.ProportionalSlashingMultiplier()
	if err != nil {
		return errors.Wrap(err, "could not get proportional slashing multiplier")
	}
	currentEpoch := time.CurrentEpoch(st)
	totalBalance, err := helpers.TotalActiveBalance(st)
	if err != nil {
		return errors.Wrap(err, "could not get total active balance")
	}

	// Compute slashed balances in the current epoch
	exitLength := params.BeaconConfig().EpochsPerSlashingsVector

	// Compute the sum of state slashings
	slashings := st.Slashings()
	totalSlashing := uint64(0)
	for _, slashing := range slashings {
		totalSlashing, err = math.Add64(totalSlashing, slashing)
		if err != nil {
			return err
		}
	}

	// a callback is used here to apply the following actions to all validators
	// below equally.
	increment := params.BeaconConfig().EffectiveBalanceIncrement
	minSlashing := min(totalSlashing*slashingMultiplier, totalBalance)

	// Modified in Electra:EIP7251
	var penaltyPerEffectiveBalanceIncrement uint64
	if st.Version() >= version.Electra {
		penaltyPerEffectiveBalanceIncrement = minSlashing / (totalBalance / increment)
	}

	bals := st.Balances()
	changed := false
	err = st.ReadFromEveryValidator(func(idx int, val state.ReadOnlyValidator) error {
		correctEpoch := (currentEpoch + exitLength/2) == val.WithdrawableEpoch()
		if val.Slashed() && correctEpoch {
			var penalty uint64
			if st.Version() >= version.Electra {
				effectiveBalanceIncrements := val.EffectiveBalance() / increment
				penalty = penaltyPerEffectiveBalanceIncrement * effectiveBalanceIncrements
			} else {
				penaltyNumerator := val.EffectiveBalance() / increment * minSlashing
				penalty = penaltyNumerator / totalBalance * increment
			}
			bals[idx] = helpers.DecreaseBalanceWithVal(bals[idx], penalty)
			changed = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if changed {
		if err := st.SetBalances(bals); err != nil {
			return err
		}
	}
	return nil
}

// ProcessEth1DataReset processes updates to ETH1 data votes during epoch processing.
//
// Spec pseudocode definition:
//
//	def process_eth1_data_reset(state: BeaconState) -> None:
//	  next_epoch = Epoch(get_current_epoch(state) + 1)
//	  # Reset eth1 data votes
//	  if next_epoch % EPOCHS_PER_ETH1_VOTING_PERIOD == 0:
//	      state.eth1_data_votes = []
func ProcessEth1DataReset(state state.BeaconState) (state.BeaconState, error) {
	currentEpoch := time.CurrentEpoch(state)
	nextEpoch := currentEpoch + 1

	// Reset ETH1 data votes.
	if nextEpoch%params.BeaconConfig().EpochsPerEth1VotingPeriod == 0 {
		if err := state.SetEth1DataVotes([]*ethpb.Eth1Data{}); err != nil {
			return nil, err
		}
	}

	return state, nil
}

// ProcessEffectiveBalanceUpdates processes effective balance updates during epoch processing.
//
// Spec pseudocode definition:
//
//	def process_effective_balance_updates(state: BeaconState) -> None:
//	  # Update effective balances with hysteresis
//	  for index, validator in enumerate(state.validators):
//	      balance = state.balances[index]
//	      HYSTERESIS_INCREMENT = uint64(EFFECTIVE_BALANCE_INCREMENT // HYSTERESIS_QUOTIENT)
//	      DOWNWARD_THRESHOLD = HYSTERESIS_INCREMENT * HYSTERESIS_DOWNWARD_MULTIPLIER
//	      UPWARD_THRESHOLD = HYSTERESIS_INCREMENT * HYSTERESIS_UPWARD_MULTIPLIER
//	      if (
//	          balance + DOWNWARD_THRESHOLD < validator.effective_balance
//	          or validator.effective_balance + UPWARD_THRESHOLD < balance
//	      ):
//	          validator.effective_balance = min(balance - balance % EFFECTIVE_BALANCE_INCREMENT, MAX_EFFECTIVE_BALANCE)
func ProcessEffectiveBalanceUpdates(st state.BeaconState) (state.BeaconState, error) {
	effBalanceInc := params.BeaconConfig().EffectiveBalanceIncrement
	maxEffBalance := params.BeaconConfig().MaxEffectiveBalance
	hysteresisInc := effBalanceInc / params.BeaconConfig().HysteresisQuotient
	downwardThreshold := hysteresisInc * params.BeaconConfig().HysteresisDownwardMultiplier
	upwardThreshold := hysteresisInc * params.BeaconConfig().HysteresisUpwardMultiplier

	bals := st.Balances()

	// Update effective balances with hysteresis.
	validatorFunc := func(idx int, val state.ReadOnlyValidator) (newVal *ethpb.Validator, err error) {
		if val == nil {
			return nil, fmt.Errorf("validator %d is nil in state", idx)
		}
		if idx >= len(bals) {
			return nil, fmt.Errorf("validator index exceeds validator length in state %d >= %d", idx, len(st.Balances()))
		}
		balance := bals[idx]

		if balance+downwardThreshold < val.EffectiveBalance() || val.EffectiveBalance()+upwardThreshold < balance {
			effectiveBal := min(maxEffBalance, balance-balance%effBalanceInc)
			if effectiveBal != val.EffectiveBalance() {
				newVal = val.Copy()
				newVal.EffectiveBalance = effectiveBal
			}
		}
		return
	}

	if err := st.ApplyToEveryValidator(validatorFunc); err != nil {
		return nil, err
	}

	return st, nil
}

// ProcessSlashingsReset processes the total slashing balances updates during epoch processing.
//
// Spec pseudocode definition:
//
//	def process_slashings_reset(state: BeaconState) -> None:
//	  next_epoch = Epoch(get_current_epoch(state) + 1)
//	  # Reset slashings
//	  state.slashings[next_epoch % EPOCHS_PER_SLASHINGS_VECTOR] = Gwei(0)
func ProcessSlashingsReset(state state.BeaconState) (state.BeaconState, error) {
	currentEpoch := time.CurrentEpoch(state)
	nextEpoch := currentEpoch + 1

	// Set total slashed balances.
	slashedExitLength := params.BeaconConfig().EpochsPerSlashingsVector
	slashedEpoch := nextEpoch % slashedExitLength
	slashings := state.Slashings()
	if uint64(len(slashings)) != uint64(slashedExitLength) {
		return nil, fmt.Errorf(
			"state slashing length %d different than EpochsPerHistoricalVector %d",
			len(slashings),
			slashedExitLength,
		)
	}
	if err := state.UpdateSlashingsAtIndex(uint64(slashedEpoch) /* index */, 0 /* value */); err != nil {
		return nil, err
	}

	return state, nil
}

// ProcessRandaoMixesReset processes the final updates to RANDAO mix during epoch processing.
//
// Spec pseudocode definition:
//
//	def process_randao_mixes_reset(state: BeaconState) -> None:
//	  current_epoch = get_current_epoch(state)
//	  next_epoch = Epoch(current_epoch + 1)
//	  # Set randao mix
//	  state.randao_mixes[next_epoch % EPOCHS_PER_HISTORICAL_VECTOR] = get_randao_mix(state, current_epoch)
func ProcessRandaoMixesReset(state state.BeaconState) (state.BeaconState, error) {
	currentEpoch := time.CurrentEpoch(state)
	nextEpoch := currentEpoch + 1

	// Set RANDAO mix.
	randaoMixLength := params.BeaconConfig().EpochsPerHistoricalVector
	if uint64(state.RandaoMixesLength()) != uint64(randaoMixLength) {
		return nil, fmt.Errorf(
			"state randao length %d different than EpochsPerHistoricalVector %d",
			state.RandaoMixesLength(),
			randaoMixLength,
		)
	}
	mix, err := helpers.RandaoMix(state, currentEpoch)
	if err != nil {
		return nil, err
	}
	if err := state.UpdateRandaoMixesAtIndex(uint64(nextEpoch%randaoMixLength), [32]byte(mix)); err != nil {
		return nil, err
	}

	return state, nil
}

// ProcessHistoricalDataUpdate processes the updates to historical data during epoch processing.
// From Capella onward, per spec,state's historical summaries are updated instead of historical roots.
func ProcessHistoricalDataUpdate(state state.BeaconState) (state.BeaconState, error) {
	currentEpoch := time.CurrentEpoch(state)
	nextEpoch := currentEpoch + 1

	// Set historical root accumulator.
	epochsPerHistoricalRoot := params.BeaconConfig().SlotsPerHistoricalRoot.DivSlot(params.BeaconConfig().SlotsPerEpoch)
	if nextEpoch.Mod(uint64(epochsPerHistoricalRoot)) == 0 {
		if state.Version() >= version.Capella {
			br, err := stateutil.ArraysRoot(state.BlockRoots(), fieldparams.BlockRootsLength)
			if err != nil {
				return nil, err
			}
			sr, err := stateutil.ArraysRoot(state.StateRoots(), fieldparams.StateRootsLength)
			if err != nil {
				return nil, err
			}
			if err := state.AppendHistoricalSummaries(&ethpb.HistoricalSummary{BlockSummaryRoot: br[:], StateSummaryRoot: sr[:]}); err != nil {
				return nil, err
			}
		} else {
			historicalBatch := &ethpb.HistoricalBatch{
				BlockRoots: state.BlockRoots(),
				StateRoots: state.StateRoots(),
			}
			batchRoot, err := historicalBatch.HashTreeRoot()
			if err != nil {
				return nil, errors.Wrap(err, "could not hash historical batch")
			}
			if err := state.AppendHistoricalRoots(batchRoot); err != nil {
				return nil, err
			}
		}
	}

	return state, nil
}

// ProcessParticipationRecordUpdates rotates current/previous epoch attestations during epoch processing.
//
// nolint:dupword
// Spec pseudocode definition:
//
//	def process_participation_record_updates(state: BeaconState) -> None:
//	  # Rotate current/previous epoch attestations
//	  state.previous_epoch_attestations = state.current_epoch_attestations
//	  state.current_epoch_attestations = []
func ProcessParticipationRecordUpdates(state state.BeaconState) (state.BeaconState, error) {
	if err := state.RotateAttestations(); err != nil {
		return nil, err
	}
	return state, nil
}

// ProcessFinalUpdates processes the final updates during epoch processing.
func ProcessFinalUpdates(state state.BeaconState) (state.BeaconState, error) {
	var err error

	// Reset ETH1 data votes.
	state, err = ProcessEth1DataReset(state)
	if err != nil {
		return nil, err
	}

	// Update effective balances with hysteresis.
	state, err = ProcessEffectiveBalanceUpdates(state)
	if err != nil {
		return nil, err
	}

	// Set total slashed balances.
	state, err = ProcessSlashingsReset(state)
	if err != nil {
		return nil, err
	}

	// Set RANDAO mix.
	state, err = ProcessRandaoMixesReset(state)
	if err != nil {
		return nil, err
	}

	// Set historical root accumulator.
	state, err = ProcessHistoricalDataUpdate(state)
	if err != nil {
		return nil, err
	}

	// Rotate current and previous epoch attestations.
	state, err = ProcessParticipationRecordUpdates(state)
	if err != nil {
		return nil, err
	}

	return state, nil
}
