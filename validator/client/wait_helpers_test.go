package client

import (
	"context"
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/testing/assert"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/OffchainLabs/prysm/v7/time/slots"
)

func TestSlotComponentDeadline(t *testing.T) {
	params.SetupTestConfigCleanup(t)

	cfg := params.BeaconConfig()
	v := &validator{genesisTime: time.Unix(1700000000, 0)}
	slot := primitives.Slot(5)
	component := cfg.AttestationDueBPS

	got, err := v.slotComponentDeadline(slot, component)
	require.NoError(t, err)

	startTime, err := slots.StartTime(v.genesisTime, slot)
	require.NoError(t, err)
	expected := startTime.Add(cfg.SlotComponentDuration(component))

	require.Equal(t, expected, got)
}

// After a scheduled slot-time change the deadline must be a fraction of the
// slot's own (new) length, not of the genesis length.
func TestSlotComponentDeadline_SlotTimeSchedule(t *testing.T) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = 100
	cfg.SlotTimeForkOneMillis = 2000
	params.SetActiveTestCleanup(t, cfg)
	v := &validator{genesisTime: time.Unix(1700000000, 0)}

	tests := []struct {
		name      string
		slot      primitives.Slot
		component primitives.BP
		expected  time.Duration
	}{
		{"attestation before the change", 5, cfg.AttestationDueBPS, 1999 * time.Millisecond},
		{"attestation after the change", 150, cfg.AttestationDueBPS, 666 * time.Millisecond},
		{"aggregate after the change", 150, cfg.AggregateDueBPS, 1333 * time.Millisecond},
		{"sync message after the change", 150, cfg.SyncMessageDueBPS, 666 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := v.slotComponentDeadline(tt.slot, tt.component)
			require.NoError(t, err)
			startTime, err := slots.StartTime(v.genesisTime, tt.slot)
			require.NoError(t, err)
			require.Equal(t, startTime.Add(tt.expected), got)
		})
	}
	// The genesis-length figure the old code would have used after the change.
	require.Equal(t, 1999*time.Millisecond, cfg.SlotComponentDuration(cfg.AttestationDueBPS))
}

func TestSlotComponentSpanName(t *testing.T) {
	params.SetupTestConfigCleanup(t)

	cfg := params.BeaconConfig()
	v := &validator{}
	tests := []struct {
		name      string
		component primitives.BP
		expected  string
	}{
		{
			name:      "attestation",
			component: cfg.AttestationDueBPS,
			expected:  "validator.waitAttestationWindow",
		},
		{
			name:      "aggregate",
			component: cfg.AggregateDueBPS,
			expected:  "validator.waitAggregateWindow",
		},
		{
			name:      "default",
			component: cfg.AttestationDueBPS + 7,
			expected:  "validator.waitSlotComponent",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, v.slotComponentSpanName(tt.component))
		})
	}
}

func TestWaitUntilSlotComponent_ContextCancelReturnsImmediately(t *testing.T) {
	params.SetupTestConfigCleanup(t)
	cfg := params.BeaconConfig().Copy()
	cfg.SlotDurationMilliseconds = 10000
	params.OverrideBeaconConfig(cfg)

	v := &validator{genesisTime: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		v.waitUntilSlotComponent(ctx, 1, cfg.AttestationDueBPS)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitUntilSlotComponent did not return after context cancellation")
	}
}
