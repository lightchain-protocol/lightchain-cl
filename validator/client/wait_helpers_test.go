package client

import (
	"context"
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/time/slots"
)

// Without the LightChain offset keys the deadlines are the spec fractions of
// the slot: a third for attestations, two thirds for aggregates.
func TestDutyDeadline_DefaultsToSpecFractions(t *testing.T) {
	params.SetupTestConfigCleanup(t)

	cfg := params.BeaconConfig()
	v := &validator{genesisTime: time.Unix(1700000000, 0)}
	slot := primitives.Slot(5)
	startTime, err := slots.StartTime(v.genesisTime, slot)
	require.NoError(t, err)

	got, err := v.dutyDeadline(slot, attestationDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(cfg.SlotComponentDuration(cfg.AttestationDueBPS)), got)

	got, err = v.dutyDeadline(slot, aggregationDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(cfg.SlotComponentDuration(cfg.AggregateDueBPS)), got)

	got, err = v.dutyDeadline(slot, contributionDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(cfg.SlotComponentDuration(cfg.ContributionDueBPS)), got)
}

// ATTESTATION_OFFSET_MS / AGGREGATION_OFFSET_MS replace the fractions; sync
// committee contributions follow the aggregation offset.
func TestDutyDeadline_ConfiguredOffsets(t *testing.T) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 2
	cfg.SlotDurationMilliseconds = 2000
	cfg.AttestationOffsetMs = 1000
	cfg.AggregationOffsetMs = 1500
	params.SetActiveTestCleanup(t, cfg)
	v := &validator{genesisTime: time.Unix(1700000000, 0)}
	slot := primitives.Slot(5)
	startTime, err := slots.StartTime(v.genesisTime, slot)
	require.NoError(t, err)

	got, err := v.dutyDeadline(slot, attestationDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(time.Second), got)

	got, err = v.dutyDeadline(slot, aggregationDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(1500*time.Millisecond), got)

	got, err = v.dutyDeadline(slot, contributionDue)
	require.NoError(t, err)
	require.Equal(t, startTime.Add(1500*time.Millisecond), got)
}

// After a scheduled slot-time change the deadline must be a fraction of the
// slot's own (new) length, not of the genesis length.
func TestDutyDeadline_SlotTimeSchedule(t *testing.T) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = 100
	cfg.SlotTimeForkOneMillis = 2000
	params.SetActiveTestCleanup(t, cfg)
	v := &validator{genesisTime: time.Unix(1700000000, 0)}

	tests := []struct {
		name     string
		slot     primitives.Slot
		duty     dutyOffset
		expected time.Duration
	}{
		{"attestation before the change", 5, attestationDue, 1999 * time.Millisecond},
		{"attestation after the change", 150, attestationDue, 666 * time.Millisecond},
		{"aggregate after the change", 150, aggregationDue, 1333 * time.Millisecond},
		{"contribution after the change", 150, contributionDue, 1333 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := v.dutyDeadline(tt.slot, tt.duty)
			require.NoError(t, err)
			startTime, err := slots.StartTime(v.genesisTime, tt.slot)
			require.NoError(t, err)
			require.Equal(t, startTime.Add(tt.expected), got)
		})
	}
	// The genesis-length figure the old code would have used after the change.
	require.Equal(t, 1999*time.Millisecond, cfg.SlotComponentDuration(cfg.AttestationDueBPS))
}

func TestDutySpanNames(t *testing.T) {
	require.Equal(t, "validator.waitAttestationWindow", attestationDue.spanName)
	require.Equal(t, "validator.waitAggregateWindow", aggregationDue.spanName)
	require.Equal(t, "validator.waitContributionWindow", contributionDue.spanName)
}

func TestWaitUntilDutyDue_ContextCancelReturnsImmediately(t *testing.T) {
	params.SetupTestConfigCleanup(t)
	cfg := params.BeaconConfig().Copy()
	cfg.SlotDurationMilliseconds = 10000
	params.OverrideBeaconConfig(cfg)

	v := &validator{genesisTime: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		v.waitUntilDutyDue(ctx, 1, attestationDue)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitUntilDutyDue did not return after context cancellation")
	}
}
