package client

import (
	"context"
	"time"

	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing/trace"
	prysmTime "github.com/OffchainLabs/prysm/v7/time"
	"github.com/OffchainLabs/prysm/v7/time/slots"
)

// dutyOffset names a point within a slot at which a duty is due. Each one
// resolves through time/slots so the validator client and the beacon node
// derive every deadline from the same chain-config values
// (ATTESTATION_OFFSET_MS / AGGREGATION_OFFSET_MS when set, otherwise the spec
// basis-point fractions of the slot's own length).
type dutyOffset struct {
	spanName string
	at       func(primitives.Slot) time.Duration
}

var (
	attestationDue  = dutyOffset{spanName: "validator.waitAttestationWindow", at: slots.AttestationDueAt}
	aggregationDue  = dutyOffset{spanName: "validator.waitAggregateWindow", at: slots.AggregationDueAt}
	contributionDue = dutyOffset{spanName: "validator.waitContributionWindow", at: slots.ContributionDueAt}
)

// dutyDeadline returns the absolute time at which the duty is due in the given slot.
//
// The offset is taken against the slot's own start and length from the
// slot-duration schedule. On a chain that has scheduled a slot-time change a
// fraction of the genesis slot length would push every attestation, aggregate
// and sync-committee deadline one or more slots late once the change is in
// effect.
func (v *validator) dutyDeadline(slot primitives.Slot, duty dutyOffset) (time.Time, error) {
	startTime, err := slots.StartTime(v.genesisTime, slot)
	if err != nil {
		return time.Time{}, err
	}
	return startTime.Add(duty.at(slot)), nil
}

// waitUntilDutyDue blocks until the duty is due in the given slot, or the
// context is done. It returns at once if the deadline has already passed.
func (v *validator) waitUntilDutyDue(ctx context.Context, slot primitives.Slot, duty dutyOffset) {
	ctx, span := trace.StartSpan(ctx, duty.spanName)
	defer span.End()

	finalTime, err := v.dutyDeadline(slot, duty)
	if err != nil {
		log.WithError(err).WithField("slot", slot).Error("Slot overflows, unable to wait for duty deadline")
		return
	}
	wait := prysmTime.Until(finalTime)
	if wait <= 0 {
		return
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		tracing.AnnotateError(span, ctx.Err())
		return
	case <-t.C:
		return
	}
}
