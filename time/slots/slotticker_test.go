package slots

import (
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/stretchr/testify/require"
)

var _ Ticker = (*SlotTicker)(nil)

func TestSlotTicker(t *testing.T) {
	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	defer ticker.Done()

	var sinceDuration time.Duration
	since := func(time.Time) time.Duration {
		return sinceDuration
	}

	var untilDuration time.Duration
	until := func(time.Time) time.Duration {
		return untilDuration
	}

	var tick chan time.Time
	after := func(time.Duration) <-chan time.Time {
		return tick
	}

	genesisTime := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	secondsPerSlot := uint64(8)

	// Test when the ticker starts immediately after genesis time.
	sinceDuration = 1 * time.Second
	untilDuration = 7 * time.Second
	// Make this a buffered channel to prevent a deadlock since
	// the other goroutine calls a function in this goroutine.
	tick = make(chan time.Time, 2)
	ticker.start(genesisTime, 0, secondsPerSlot, since, until, after)

	// Tick once.
	tick <- time.Now()
	slot := <-ticker.C()
	if slot != 0 {
		t.Fatalf("Expected %d, got %d", 0, slot)
	}

	// Tick twice.
	tick <- time.Now()
	slot = <-ticker.C()
	if slot != 1 {
		t.Fatalf("Expected %d, got %d", 1, slot)
	}

	// Tick thrice.
	tick <- time.Now()
	slot = <-ticker.C()
	if slot != 2 {
		t.Fatalf("Expected %d, got %d", 2, slot)
	}
}

func TestSlotTickerGenesis(t *testing.T) {
	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	defer ticker.Done()

	var sinceDuration time.Duration
	since := func(time.Time) time.Duration {
		return sinceDuration
	}

	var untilDuration time.Duration
	until := func(time.Time) time.Duration {
		return untilDuration
	}

	var tick chan time.Time
	after := func(time.Duration) <-chan time.Time {
		return tick
	}

	genesisTime := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	secondsPerSlot := uint64(8)

	// Test when the ticker starts before genesis time.
	sinceDuration = -1 * time.Second
	untilDuration = 1 * time.Second
	// Make this a buffered channel to prevent a deadlock since
	// the other goroutine calls a function in this goroutine.
	tick = make(chan time.Time, 2)
	ticker.start(genesisTime, 0, secondsPerSlot, since, until, after)

	// Tick once.
	tick <- time.Now()
	slot := <-ticker.C()
	if slot != 0 {
		t.Fatalf("Expected %d, got %d", 0, slot)
	}

	// Tick twice.
	tick <- time.Now()
	slot = <-ticker.C()
	if slot != 1 {
		t.Fatalf("Expected %d, got %d", 1, slot)
	}
}

func TestGetSlotTickerWithOffset_OK(t *testing.T) {
	genesisTime := time.Now()
	secondsPerSlot := uint64(4)
	offset := time.Duration(secondsPerSlot/2) * time.Second

	offsetTicker := NewSlotTickerWithOffset(genesisTime, offset, secondsPerSlot)
	normalTicker := NewSlotTicker(genesisTime, secondsPerSlot)

	firstTicked := 0
	for {
		select {
		case <-offsetTicker.C():
			if firstTicked != 1 {
				t.Fatal("Expected other ticker to tick first")
			}
			return
		case <-normalTicker.C():
			if firstTicked != 0 {
				t.Fatal("Expected normal ticker to tick first")
			}
			firstTicked = 1
		}
	}
}

func TestGetSlotTickerWitIntervals(t *testing.T) {
	genesisTime := time.Now()
	offset := time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Second / 3
	intervals := []time.Duration{offset, 2 * offset}

	intervalTicker := NewSlotTickerWithIntervals(genesisTime, intervals)
	normalTicker := NewSlotTicker(genesisTime, params.BeaconConfig().SecondsPerSlot)

	firstTicked := 0
	for {
		select {
		case <-intervalTicker.C():
			// interval ticks starts in second slot
			if firstTicked < 2 {
				t.Fatal("Expected other ticker to tick first")
			}
			return
		case <-normalTicker.C():
			if firstTicked > 1 {
				t.Fatal("Expected normal ticker to tick first")
			}
			firstTicked++
		}
	}
}

func TestSlotTickerWithIntervalsInputValidation(t *testing.T) {
	var genesisTime time.Time
	offset := time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Second / 3
	intervals := make([]time.Duration, 0)
	panicCall := func() {
		NewSlotTickerWithIntervals(genesisTime, intervals)
	}
	require.Panics(t, panicCall, "zero genesis time")
	genesisTime = time.Now()
	require.Panics(t, panicCall, "at least one interval has to be entered")
	intervals = []time.Duration{2 * offset, offset}
	require.Panics(t, panicCall, "invalid decreasing offsets")
	intervals = []time.Duration{offset, 4 * offset}
	require.Panics(t, panicCall, "invalid ticker offset")
	intervals = []time.Duration{4 * offset, offset}
	require.Panics(t, panicCall, "invalid ticker offset")
	intervals = []time.Duration{offset, 2 * offset}
	require.NotPanics(t, panicCall)
}

// cutConfigAt installs a 6 s base slot with a scheduled change to 2 s slots at
// cutSlot, and returns a genesis time such that now is the start of nowSlot.
func cutConfigAt(t *testing.T, cutSlot, nowSlot uint64, now time.Time) time.Time {
	cfg := params.BeaconConfig().Copy()
	cfg.SecondsPerSlot = 6
	cfg.SlotDurationMilliseconds = 6000
	cfg.SlotTimeForkOneSlot = cutSlot
	cfg.SlotTimeForkOneMillis = 2000
	cfg.SlotTimeForkTwoSlot = 0
	cfg.SlotTimeForkTwoMillis = 0
	params.SetActiveTestCleanup(t, cfg)
	elapsed := time.Duration(cutSlot)*6*time.Second + time.Duration(nowSlot-cutSlot)*2*time.Second
	return now.Add(-elapsed)
}

func TestScaleToSlot(t *testing.T) {
	// No schedule: the offset is what the caller asked for.
	require.Equal(t, 4*time.Second, scaleToSlot(4*time.Second, 50))

	cutConfigAt(t, 100, 100, time.Now())
	require.Equal(t, 4*time.Second, scaleToSlot(4*time.Second, 50), "before the cut")
	require.Equal(t, 4*time.Second, scaleToSlot(4*time.Second, 99), "last 6 s slot")
	require.Equal(t, 4*time.Second/3, scaleToSlot(4*time.Second, 100), "first 2 s slot")
	require.Equal(t, 2*time.Second/3, scaleToSlot(2*time.Second, 250))
	require.Equal(t, time.Duration(0), scaleToSlot(0, 250))
}

// After the cut an interval expressed against the 6 s base (4 s, as the
// attestation routine uses) must land inside the 2 s slot, and every slot's
// first tick must be exactly at that slot's start. On the unscaled ticker the
// 4 s interval pushes each following tick a whole slot late.
func TestSlotTickerWithIntervals_TicksStayInsideShorterSlot(t *testing.T) {
	now := time.Now()
	genesis := cutConfigAt(t, 10, 20, now)
	ticker := &SlotIntervalTicker{c: make(chan SlotInterval), done: make(chan struct{})}
	defer ticker.Done()

	dueCh := make(chan time.Time, 16)
	until := func(tm time.Time) time.Duration { dueCh <- tm; return 0 }
	tick := make(chan time.Time, 8)
	after := func(time.Duration) <-chan time.Time { return tick }
	ticker.startWithIntervals(genesis, until, after, []time.Duration{0, 4 * time.Second})

	var prev time.Time
	for i := 0; i < 6; i++ {
		due := <-dueCh
		tick <- now
		got := <-ticker.C()
		require.True(t, got.Slot >= 21, "ticks must be in the 2 s segment, got slot %d", got.Slot)
		require.Equal(t, i%2, got.Interval)
		if i > 0 {
			require.True(t, due.After(prev), "tick times must strictly increase: %v then %v", prev, due)
		}
		start, err := StartTime(genesis, got.Slot)
		require.NoError(t, err)
		if got.Interval == 0 {
			require.Equal(t, start, due, "first tick of slot %d at its start", got.Slot)
		} else {
			require.Equal(t, start.Add(4*time.Second/3), due, "4 s of a 6 s slot is 1.33 s of a 2 s slot")
		}
		prev = due
	}
}

// The offset ticker (lateBlockTasks uses SecondsPerSlot/3 = 2 s on the 6 s
// base) must fire a third into each 2 s slot after the cut, not at the start
// of the next slot.
func TestSlotTickerWithOffset_ScaledAfterCut(t *testing.T) {
	now := time.Now()
	genesis := cutConfigAt(t, 10, 20, now)
	ticker := &SlotTicker{c: make(chan primitives.Slot), done: make(chan struct{})}
	defer ticker.Done()

	since := func(tm time.Time) time.Duration { return now.Sub(tm) }
	dueCh := make(chan time.Time, 16)
	until := func(tm time.Time) time.Duration { dueCh <- tm; return 0 }
	tick := make(chan time.Time, 8)
	after := func(time.Duration) <-chan time.Time { return tick }
	ticker.start(genesis, 2*time.Second, 6, since, until, after)

	for i := 0; i < 3; i++ {
		due := <-dueCh
		tick <- now
		slot := <-ticker.C()
		require.Equal(t, primitives.Slot(20+i), slot)
		start, err := StartTime(genesis, slot)
		require.NoError(t, err)
		require.Equal(t, start.Add(2*time.Second/3), due)
	}
}

// Without a schedule the offset ticker keeps its original arithmetic.
func TestSlotTickerWithOffset_NoScheduleUnchanged(t *testing.T) {
	ticker := &SlotTicker{c: make(chan primitives.Slot), done: make(chan struct{})}
	defer ticker.Done()
	genesis := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	since := func(time.Time) time.Duration { return 9 * time.Second } // 9 s after genesis+offset
	dueCh := make(chan time.Time, 16)
	until := func(tm time.Time) time.Duration { dueCh <- tm; return 0 }
	tick := make(chan time.Time, 8)
	after := func(time.Duration) <-chan time.Time { return tick }
	ticker.start(genesis, 3*time.Second, 8, since, until, after)

	due := <-dueCh
	tick <- time.Now()
	slot := <-ticker.C()
	require.Equal(t, primitives.Slot(2), slot)
	require.Equal(t, genesis.Add(3*time.Second+16*time.Second), due)
}
