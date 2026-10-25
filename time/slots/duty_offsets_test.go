package slots

import (
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/testing/require"
)

func withTwoSecondSlots(t *testing.T, attMs, aggMs uint64) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 2
	cfg.SlotDurationMilliseconds = 2000
	cfg.AttestationOffsetMs = attMs
	cfg.AggregationOffsetMs = aggMs
	params.SetActiveTestCleanup(t, cfg)
}

func TestDutyDueAt_DefaultsToThirds(t *testing.T) {
	withTwoSecondSlots(t, 0, 0)
	require.Equal(t, 666*time.Millisecond, AttestationDueAt(7))
	require.Equal(t, 1333*time.Millisecond, AggregationDueAt(7))
	require.Equal(t, 666*time.Millisecond, SyncMessageDueAt(7))
	require.Equal(t, 1333*time.Millisecond, ContributionDueAt(7))
	// Same figure the legacy helper gives on a single-segment chain.
	require.Equal(t, params.BeaconConfig().SlotComponentDuration(params.BeaconConfig().AttestationDueBPS), AttestationDueAt(7))
}

func TestDutyDueAt_UsesConfiguredOffsets(t *testing.T) {
	withTwoSecondSlots(t, 1000, 1500)
	require.Equal(t, time.Second, AttestationDueAt(7))
	require.Equal(t, 1500*time.Millisecond, AggregationDueAt(7))
	require.Equal(t, time.Second, SyncMessageDueAt(7))
	require.Equal(t, 1500*time.Millisecond, ContributionDueAt(7))
}

// On a chain with a slot-time schedule the fraction follows the slot's own
// length while a millisecond offset stays absolute.
func TestDutyDueAt_SlotTimeSchedule(t *testing.T) {
	withSchedule(t) // 6s, 3s from slot 100, 1s from slot 200
	require.Equal(t, 1999*time.Millisecond, AttestationDueAt(50))
	require.Equal(t, 999*time.Millisecond, AttestationDueAt(150))
	require.Equal(t, 333*time.Millisecond, AttestationDueAt(250))
	require.Equal(t, 666*time.Millisecond, AggregationDueAt(250))

	cfg := params.BeaconConfig().Copy()
	cfg.AttestationOffsetMs = 500
	cfg.AggregationOffsetMs = 750
	params.SetActiveTestCleanup(t, cfg)
	for _, slot := range []primitives.Slot{50, 150, 250} {
		require.Equal(t, 500*time.Millisecond, AttestationDueAt(slot))
		require.Equal(t, 750*time.Millisecond, AggregationDueAt(slot))
	}
}

func TestWithinVotingWindow_UsesAttestationOffset(t *testing.T) {
	withTwoSecondSlots(t, 1000, 1500)
	// 800 ms into slot 3: inside the 1000 ms window, outside the legacy 666 ms one.
	genesis := time.Now().Add(-(3*2*time.Second + 800*time.Millisecond))
	require.Equal(t, true, WithinVotingWindow(genesis, 3))
	// 1200 ms in: past the deadline.
	genesis = time.Now().Add(-(3*2*time.Second + 1200*time.Millisecond))
	require.Equal(t, false, WithinVotingWindow(genesis, 3))
}

// The offset-function ticker fires at StartTime(slot)+offset(slot) without
// rescaling the offset, picking up from the slot in progress.
func TestSlotTickerWithOffsetFunc(t *testing.T) {
	withTwoSecondSlots(t, 1000, 1500)
	genesis := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	defer ticker.Done()

	// 10.5 s after genesis: slot 5 started at 10 s, its attestation deadline
	// (11 s) is still ahead, so the first tick must be slot 5 at 11 s.
	now := genesis.Add(10500 * time.Millisecond)
	since := func(tm time.Time) time.Duration { return now.Sub(tm) }
	var waits []time.Duration
	tick := make(chan time.Time, 3)
	until := func(tm time.Time) time.Duration {
		d := tm.Sub(now)
		waits = append(waits, d)
		return d
	}
	after := func(time.Duration) <-chan time.Time { return tick }

	ticker.startWithOffsetFunc(genesis, AttestationDueAt, since, until, after)

	tick <- time.Now()
	require.Equal(t, primitives.Slot(5), <-ticker.C())
	tick <- time.Now()
	require.Equal(t, primitives.Slot(6), <-ticker.C())
	tick <- time.Now()
	require.Equal(t, primitives.Slot(7), <-ticker.C())
	// Waits are measured from the fixed "now": 11 s, 13 s, 15 s after genesis.
	require.Equal(t, 500*time.Millisecond, waits[0])
	require.Equal(t, 2500*time.Millisecond, waits[1])
	require.Equal(t, 4500*time.Millisecond, waits[2])
}

func TestSlotTickerWithOffsetFunc_DeadlinePassedMovesToNextSlot(t *testing.T) {
	withTwoSecondSlots(t, 1000, 1500)
	genesis := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	defer ticker.Done()

	// 11.2 s after genesis: slot 5's deadline (11 s) is behind us.
	now := genesis.Add(11200 * time.Millisecond)
	since := func(tm time.Time) time.Duration { return now.Sub(tm) }
	var firstWait time.Duration
	until := func(tm time.Time) time.Duration {
		if firstWait == 0 {
			firstWait = tm.Sub(now)
		}
		return tm.Sub(now)
	}
	tick := make(chan time.Time, 1)
	after := func(time.Duration) <-chan time.Time { return tick }

	ticker.startWithOffsetFunc(genesis, AttestationDueAt, since, until, after)
	tick <- time.Now()
	require.Equal(t, primitives.Slot(6), <-ticker.C())
	require.Equal(t, 1800*time.Millisecond, firstWait)
}

func TestSlotTickerWithOffsetFunc_BeforeGenesis(t *testing.T) {
	withTwoSecondSlots(t, 1000, 1500)
	genesis := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	defer ticker.Done()

	now := genesis.Add(-5 * time.Second)
	since := func(tm time.Time) time.Duration { return now.Sub(tm) }
	var firstWait time.Duration
	until := func(tm time.Time) time.Duration {
		if firstWait == 0 {
			firstWait = tm.Sub(now)
		}
		return tm.Sub(now)
	}
	tick := make(chan time.Time, 1)
	after := func(time.Duration) <-chan time.Time { return tick }

	ticker.startWithOffsetFunc(genesis, AttestationDueAt, since, until, after)
	tick <- time.Now()
	require.Equal(t, primitives.Slot(0), <-ticker.C())
	require.Equal(t, 6*time.Second, firstWait)
}
