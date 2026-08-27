package slots

import (
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/testing/require"
)

// withSchedule installs a config carrying a two-step slot-time schedule:
// 6s from genesis, 3s from slot 100, 1s from slot 200.
func withSchedule(t *testing.T) time.Time {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = 100
	cfg.SlotTimeForkOneMillis = 3000
	cfg.SlotTimeForkTwoSlot = 200
	cfg.SlotTimeForkTwoMillis = 1000
	params.SetActiveTestCleanup(t, cfg)
	return time.Unix(1_700_000_000, 0)
}

func TestSlotTimeSchedule_SegmentsAreOrdered(t *testing.T) {
	withSchedule(t)
	segs := params.BeaconConfig().SlotTimeSchedule()
	require.Equal(t, 3, len(segs))
	require.Equal(t, uint64(0), segs[0].StartSlot)
	require.Equal(t, uint64(6000), segs[0].DurationMillis)
	require.Equal(t, uint64(100), segs[1].StartSlot)
	require.Equal(t, uint64(200), segs[2].StartSlot)
}

func TestSlotTimeSchedule_NoForkIsUniform(t *testing.T) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = 0
	cfg.SlotTimeForkTwoSlot = 0
	params.SetActiveTestCleanup(t, cfg)

	require.Equal(t, 1, len(params.BeaconConfig().SlotTimeSchedule()))
	genesis := time.Unix(1_700_000_000, 0)
	// Uniform behaviour: slot N starts exactly N*6s after genesis.
	for _, slot := range []primitives.Slot{0, 1, 50, 1000} {
		got, err := StartTime(genesis, slot)
		require.NoError(t, err)
		require.Equal(t, genesis.Add(time.Duration(slot)*6*time.Second), got)
	}
}

func TestStartTime_PiecewiseBoundaries(t *testing.T) {
	genesis := withSchedule(t)

	// Segment 1: 6s slots. Slot 100 begins 100*6s after genesis.
	forkOne := genesis.Add(600 * time.Second)
	got, err := StartTime(genesis, 100)
	require.NoError(t, err)
	require.Equal(t, forkOne, got)

	// Segment 2: 3s slots from slot 100. Slot 200 is 100 slots later = +300s.
	forkTwo := forkOne.Add(300 * time.Second)
	got, err = StartTime(genesis, 200)
	require.NoError(t, err)
	require.Equal(t, forkTwo, got)

	// Segment 3: 1s slots from slot 200.
	got, err = StartTime(genesis, 260)
	require.NoError(t, err)
	require.Equal(t, forkTwo.Add(60*time.Second), got)
}

func TestAt_IsInverseOfStartTime(t *testing.T) {
	genesis := withSchedule(t)
	// Across every segment, the slot containing a slot's own start time must be
	// that slot — the property the whole mechanism depends on.
	for _, slot := range []primitives.Slot{0, 1, 99, 100, 101, 199, 200, 201, 500} {
		start, err := StartTime(genesis, slot)
		require.NoError(t, err)
		require.Equal(t, slot, At(genesis, start), "slot %d did not round-trip", slot)
		// One millisecond before a slot begins still belongs to the prior slot.
		if slot > 0 {
			require.Equal(t, slot-1, At(genesis, start.Add(-time.Millisecond)))
		}
	}
}

func TestAt_IsMonotonic(t *testing.T) {
	genesis := withSchedule(t)
	prev := primitives.Slot(0)
	// Sweep 20 minutes of wall clock, which crosses both boundaries.
	for s := 0; s < 1200; s++ {
		got := At(genesis, genesis.Add(time.Duration(s)*time.Second))
		require.Equal(t, true, got >= prev, "slot went backwards at +%ds: %d -> %d", s, prev, got)
		prev = got
	}
}

func TestSlotDurationAt_PerSegment(t *testing.T) {
	withSchedule(t)
	require.Equal(t, 6*time.Second, SlotDurationAt(0))
	require.Equal(t, 6*time.Second, SlotDurationAt(99))
	require.Equal(t, 3*time.Second, SlotDurationAt(100))
	require.Equal(t, 3*time.Second, SlotDurationAt(199))
	require.Equal(t, 1*time.Second, SlotDurationAt(200))
	require.Equal(t, 1*time.Second, SlotDurationAt(10_000))
}

// Regression: SinceSlotStart derived a slot's start by multiplying by a single
// slot duration, which disagreed with At() past a boundary. Forkchoice then saw
// the current slot as starting in the future and the node refused to start with
// "invalid timestamp". Every slot At() reports for a given instant must have
// already started at that instant.
func TestSinceSlotStart_AgreesWithAt(t *testing.T) {
	genesis := withSchedule(t)
	for _, offset := range []time.Duration{
		0, 30 * time.Second, 599 * time.Second, // segment 1 (6s)
		600 * time.Second, 700 * time.Second, 899 * time.Second, // segment 2 (3s)
		900 * time.Second, 1200 * time.Second, 5000 * time.Second, // segment 3 (1s)
	} {
		now := genesis.Add(offset)
		slot := At(genesis, now)
		_, err := SinceSlotStart(slot, genesis, now)
		require.NoError(t, err, "slot %d reported at +%s had not started yet", slot, offset)
	}
}

// A boundary at or before an existing one would make the mapping non-monotonic,
// so it must be rejected rather than applied.
func TestSlotTimeSchedule_RejectsOutOfOrderBoundary(t *testing.T) {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = 200
	cfg.SlotTimeForkOneMillis = 3000
	cfg.SlotTimeForkTwoSlot = 100 // earlier than fork one
	cfg.SlotTimeForkTwoMillis = 1000
	params.SetActiveTestCleanup(t, cfg)

	segs := params.BeaconConfig().SlotTimeSchedule()
	require.Equal(t, 2, len(segs)) // the out-of-order entry is dropped
	require.Equal(t, uint64(200), segs[1].StartSlot)
}
