// Package slots includes ticker and timer-related functions for Ethereum consensus.
package slots

import (
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	prysmTime "github.com/OffchainLabs/prysm/v7/time"
)

// The Ticker interface defines a type which can expose a
// receive-only channel firing slot events.
type Ticker interface {
	C() <-chan primitives.Slot
	Done()
}

// SlotInterval is a wrapper that contains a slot and the interval index that
// triggered the ticker
type SlotInterval struct {
	Slot     primitives.Slot
	Interval int
}

// The IntervalTicker is similar to the Ticker interface but
// exposes also the interval along with the slot number
type IntervalTicker interface {
	C() <-chan SlotInterval
	Done()
}

// SlotTicker is a special ticker for the beacon chain block.
// The channel emits over the slot interval, and ensures that
// the ticks are in line with the genesis time. This means that
// the duration between the ticks and the genesis time are always a
// multiple of the slot duration.
// In addition, the channel returns the new slot number.
type SlotTicker struct {
	c    chan primitives.Slot
	done chan struct{}
}

// SlotIntervalTicker is similar to a slot ticker but it returns also
// the index of the interval that triggered the event
type SlotIntervalTicker struct {
	c    chan SlotInterval
	done chan struct{}
}

// C returns the ticker channel. Call Cancel afterwards to ensure
// that the goroutine exits cleanly.
func (s *SlotTicker) C() <-chan primitives.Slot {
	return s.c
}

// C returns the ticker channel. Call Cancel afterwards to ensure
// that the goroutine exits cleanly.
func (s *SlotIntervalTicker) C() <-chan SlotInterval {
	return s.c
}

// Done should be called to clean up the ticker.
func (s *SlotTicker) Done() {
	go func() {
		s.done <- struct{}{}
	}()
}

// Done should be called to clean up the ticker.
func (s *SlotIntervalTicker) Done() {
	go func() {
		s.done <- struct{}{}
	}()
}

// NewSlotTicker starts and returns a new SlotTicker instance.
// This method panics if genesis time is zero.
// lint:nopanic -- Communicated panic in godoc commentary.
func NewSlotTicker(genesisTime time.Time, secondsPerSlot uint64) *SlotTicker {
	if genesisTime.IsZero() {
		panic("zero genesis time")
	}
	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	ticker.start(genesisTime, 0, secondsPerSlot, prysmTime.Since, prysmTime.Until, time.After)
	return ticker
}

// NewSlotTickerWithOffset starts and returns a SlotTicker instance that allows a offset of time from genesis,
// entering a offset greater than secondsPerSlot is not allowed.
// This method will panic if genesis time is zero or the offset is less than seconds per slot.
// lint:nopanic -- Communicated panic in godoc commentary.
func NewSlotTickerWithOffset(genesisTime time.Time, offset time.Duration, secondsPerSlot uint64) *SlotTicker {
	if genesisTime.Unix() == 0 {
		panic("zero genesis time")
	}
	if offset > time.Duration(secondsPerSlot)*time.Second {
		panic("invalid ticker offset")
	}
	ticker := &SlotTicker{
		c:    make(chan primitives.Slot),
		done: make(chan struct{}),
	}
	ticker.start(genesisTime, offset, secondsPerSlot, prysmTime.Since, prysmTime.Until, time.After)
	return ticker
}

// scaleToSlot rescales an offset that a caller expressed against the base
// slot length (SecondsPerSlot / SlotDuration) to the length of the given slot,
// so the tick lands at the same fraction of the slot after a scheduled
// slot-time change. Without a schedule, or before the first boundary, the
// offset is returned unchanged. An offset shorter than the base slot stays
// shorter than the slot it is scaled to, so a valid offset never crosses into
// the next slot.
func scaleToSlot(offset time.Duration, slot primitives.Slot) time.Duration {
	segments := params.BeaconConfig().SlotTimeSchedule()
	if len(segments) == 1 || offset == 0 {
		return offset
	}
	base := time.Duration(segments[0].DurationMillis) * time.Millisecond
	d := SlotDurationAt(slot)
	if base <= 0 || d == base {
		return offset
	}
	// Millisecond ratio: exact for configured durations and safe from overflow.
	return time.Duration(int64(offset) * d.Milliseconds() / base.Milliseconds())
}

// tickTime is the wall-clock time of the tick for the slot at the given
// offset, with the offset scaled to that slot's length.
func tickTime(genesis time.Time, slot primitives.Slot, offset time.Duration) (time.Time, error) {
	t, err := StartTime(genesis, slot)
	if err != nil {
		return t, err
	}
	return t.Add(scaleToSlot(offset, slot)), nil
}

func (s *SlotTicker) start(
	genesisTime time.Time,
	offset time.Duration,
	secondsPerSlot uint64,
	since, until func(time.Time) time.Duration,
	after func(time.Duration) <-chan time.Time) {
	d := time.Duration(secondsPerSlot) * time.Second

	// Only chains that have scheduled a slot-time change need the piecewise
	// path. Everything else keeps the original fixed-interval arithmetic, which
	// also preserves the caller-supplied secondsPerSlot when it deliberately
	// differs from the global config (as some tests do). That path folds the
	// offset into the genesis time, as it always did; the piecewise path keeps
	// the true genesis and scales the offset to each slot's own length.
	scheduled := len(params.BeaconConfig().SlotTimeSchedule()) > 1
	offsetGenesis := genesisTime.Add(offset)

	go func() {
		sinceGenesis := since(offsetGenesis)

		var nextTickTime time.Time
		var slot primitives.Slot
		if sinceGenesis < d {
			// Handle when the current time is before the genesis time.
			nextTickTime = offsetGenesis
			slot = 0
		} else if scheduled {
			now := genesisTime.Add(since(genesisTime))
			slot = At(genesisTime, now)
			t, err := tickTime(genesisTime, slot, offset)
			if err == nil && !t.After(now) {
				// This slot's tick is already behind us: the next one is due.
				slot++
				t, err = tickTime(genesisTime, slot, offset)
			}
			if err != nil {
				log.WithError(err).Error("Could not compute slot start time; slot ticker stopping")
				return
			}
			nextTickTime = t
		} else {
			nextTick := sinceGenesis.Truncate(d) + d
			nextTickTime = offsetGenesis.Add(nextTick)
			slot = primitives.Slot(nextTick / d)
		}

		for {
			waitTime := until(nextTickTime)
			select {
			case <-after(waitTime):
				s.c <- slot
				slot++
				if scheduled {
					// Recompute rather than adding a constant: the interval
					// changes at the boundary, and StartTime accounts for it.
					t, err := tickTime(genesisTime, slot, offset)
					if err != nil {
						log.WithError(err).Error("Could not compute slot start time; slot ticker stopping")
						return
					}
					nextTickTime = t
				} else {
					nextTickTime = nextTickTime.Add(d)
				}
			case <-s.done:
				return
			}
		}
	}()
}

// startWithIntervals starts a ticker that emits a tick every slot at the
// prescribed intervals. The caller is responsible to make these intervals increasing and
// less than secondsPerSlot. The intervals are expressed against the base slot
// length and scaled to each slot's own length: an interval longer than the
// slot would otherwise push every later tick, including the next slot's first
// one, past its slot.
func (s *SlotIntervalTicker) startWithIntervals(
	genesisTime time.Time,
	until func(time.Time) time.Duration,
	after func(time.Duration) <-chan time.Time,
	intervals []time.Duration) {
	go func() {
		slot := CurrentSlot(genesisTime)
		slot++
		interval := 0
		nextTickTime := UnsafeStartTime(genesisTime, slot).Add(scaleToSlot(intervals[0], slot))

		for {
			waitTime := until(nextTickTime)
			select {
			case <-after(waitTime):
				s.c <- SlotInterval{Slot: slot, Interval: interval}
				interval++
				if interval == len(intervals) {
					interval = 0
					slot++
				}
				nextTickTime = UnsafeStartTime(genesisTime, slot).Add(scaleToSlot(intervals[interval], slot))
			case <-s.done:
				return
			}
		}
	}()
}

// NewSlotTickerWithIntervals starts and returns a SlotTicker instance that allows
// several offsets of time from genesis,
// Caller is responsible to input the intervals in increasing order and none bigger or equal than
// SecondsPerSlot
// This method will panic if genesis time is zero, intervals is 0 length, or offsets are invalid.
// lint:nopanic -- Communicated panic in godoc commentary.
func NewSlotTickerWithIntervals(genesisTime time.Time, intervals []time.Duration) *SlotIntervalTicker {
	if genesisTime.Unix() == 0 {
		panic("zero genesis time")
	}
	if len(intervals) == 0 {
		panic("at least one interval has to be entered")
	}
	slotDuration := time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Second
	lastOffset := time.Duration(0)
	for _, offset := range intervals {
		if offset < lastOffset {
			panic("invalid decreasing offsets")
		}
		if offset >= slotDuration {
			panic("invalid ticker offset")
		}
		lastOffset = offset
	}
	ticker := &SlotIntervalTicker{
		c:    make(chan SlotInterval),
		done: make(chan struct{}),
	}
	ticker.startWithIntervals(genesisTime, prysmTime.Until, time.After, intervals)
	return ticker
}
