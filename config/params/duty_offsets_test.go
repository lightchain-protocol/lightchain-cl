package params_test

import (
	"strings"
	"testing"
	"time"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/testing/require"
)

// Without ATTESTATION_OFFSET_MS / AGGREGATION_OFFSET_MS the due times are the
// spec basis-point fractions of the slot: thirds pre-Gloas, quarters/halves
// from Gloas.
func TestDutyDue_DefaultsToSpecFractions(t *testing.T) {
	t.Parallel()
	cfg := params.MainnetConfig().Copy()
	cfg.GloasForkEpoch = 1000

	twoSeconds := 2 * time.Second
	require.Equal(t, 666*time.Millisecond, cfg.AttestationDue(twoSeconds, 0))
	require.Equal(t, 1333*time.Millisecond, cfg.AggregationDue(twoSeconds, 0))
	require.Equal(t, 666*time.Millisecond, cfg.SyncMessageDue(twoSeconds, 0))
	require.Equal(t, 1333*time.Millisecond, cfg.ContributionDue(twoSeconds, 0))

	twelve := 12 * time.Second
	require.Equal(t, 3999*time.Millisecond, cfg.AttestationDue(twelve, 0))
	require.Equal(t, 8000*time.Millisecond, cfg.AggregationDue(twelve, 0))
	// Matches the pre-existing helper for a single-segment chain.
	require.Equal(t, cfg.SlotComponentDuration(cfg.AttestationDueBPS), cfg.AttestationDue(cfg.SlotDuration(), 0))
	require.Equal(t, cfg.SlotComponentDuration(cfg.AggregateDueBPS), cfg.AggregationDue(cfg.SlotDuration(), 0))

	// Gloas fractions from the Gloas fork epoch.
	require.Equal(t, 500*time.Millisecond, cfg.AttestationDue(twoSeconds, 1000))
	require.Equal(t, 1000*time.Millisecond, cfg.AggregationDue(twoSeconds, 1000))
	require.Equal(t, 666*time.Millisecond, cfg.AttestationDue(twoSeconds, 999))
}

func TestDutyDue_ConfiguredOffsetsWin(t *testing.T) {
	t.Parallel()
	cfg := params.MainnetConfig().Copy()
	cfg.GloasForkEpoch = 1000
	cfg.AttestationOffsetMs = 1000
	cfg.AggregationOffsetMs = 1500

	for _, d := range []time.Duration{2 * time.Second, 3 * time.Second, 12 * time.Second} {
		for _, epoch := range []primitives.Epoch{0, 999, 1000, 5000} {
			require.Equal(t, time.Second, cfg.AttestationDue(d, epoch), "attestation %s epoch %d", d, epoch)
			require.Equal(t, 1500*time.Millisecond, cfg.AggregationDue(d, epoch), "aggregation %s epoch %d", d, epoch)
			// Sync committee duties follow the same two offsets.
			require.Equal(t, time.Second, cfg.SyncMessageDue(d, epoch), "sync message %s epoch %d", d, epoch)
			require.Equal(t, 1500*time.Millisecond, cfg.ContributionDue(d, epoch), "contribution %s epoch %d", d, epoch)
		}
	}
}

// Setting only one key leaves the other duty on its spec fraction.
func TestDutyDue_OneKeyOnly(t *testing.T) {
	t.Parallel()
	cfg := params.MainnetConfig().Copy()
	cfg.AttestationOffsetMs = 1000
	require.Equal(t, time.Second, cfg.AttestationDue(2*time.Second, 0))
	require.Equal(t, 1333*time.Millisecond, cfg.AggregationDue(2*time.Second, 0))

	cfg = params.MainnetConfig().Copy()
	cfg.AggregationOffsetMs = 1500
	require.Equal(t, 666*time.Millisecond, cfg.AttestationDue(2*time.Second, 0))
	require.Equal(t, 1500*time.Millisecond, cfg.AggregationDue(2*time.Second, 0))
}

func TestValidateDutyOffsets(t *testing.T) {
	t.Parallel()
	base := func(slotMs uint64) *params.BeaconChainConfig {
		cfg := params.MainnetConfig().Copy()
		cfg.SecondsPerSlot = slotMs / 1000
		cfg.SlotDurationMilliseconds = slotMs
		return cfg
	}
	tests := []struct {
		name    string
		cfg     func() *params.BeaconChainConfig
		wantErr string
	}{
		{
			name: "absent keys always pass",
			cfg:  func() *params.BeaconChainConfig { return base(2000) },
		},
		{
			name: "2s slots half and three quarters",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1000, 1500
				return c
			},
		},
		{
			name: "3s slots half and three quarters",
			cfg: func() *params.BeaconChainConfig {
				c := base(3000)
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1500, 2250
				return c
			},
		},
		{
			name: "attestation offset at the slot end",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AttestationOffsetMs, c.AggregationOffsetMs = 2000, 2500
				return c
			},
			wantErr: "ATTESTATION_OFFSET_MS 2000 must be less than the slot duration 2000",
		},
		{
			name: "aggregation offset past the slot end",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1000, 2000
				return c
			},
			wantErr: "AGGREGATION_OFFSET_MS 2000 must be less than the slot duration 2000",
		},
		{
			name: "attestation not before aggregation",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1500, 1500
				return c
			},
			wantErr: "attestations must be due before aggregates",
		},
		{
			name: "attestation offset alone collides with the spec aggregate fraction",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AttestationOffsetMs = 1500 // aggregates stay at 2/3 = 1333 ms
				return c
			},
			wantErr: "attestations must be due before aggregates",
		},
		{
			name: "aggregation offset alone before the spec attestation fraction",
			cfg: func() *params.BeaconChainConfig {
				c := base(2000)
				c.AggregationOffsetMs = 600 // attestations stay at 1/3 = 666 ms
				return c
			},
			wantErr: "attestations must be due before aggregates",
		},
		{
			name: "offsets must fit the shortest scheduled slot",
			cfg: func() *params.BeaconChainConfig {
				c := base(6000)
				c.SlotTimeForkOneSlot, c.SlotTimeForkOneMillis = 100, 2000
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1500, 2250
				return c
			},
			wantErr: "AGGREGATION_OFFSET_MS 2250 must be less than the slot duration 2000 ms (slot 100 onward)",
		},
		{
			name: "offsets fitting every scheduled slot pass",
			cfg: func() *params.BeaconChainConfig {
				c := base(6000)
				c.SlotTimeForkOneSlot, c.SlotTimeForkOneMillis = 100, 2000
				c.AttestationOffsetMs, c.AggregationOffsetMs = 1000, 1500
				return c
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg().ValidateDutyOffsets()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.NotNil(t, err)
			require.Equal(t, true, strings.Contains(err.Error(), tt.wantErr), "error %q does not contain %q", err.Error(), tt.wantErr)
		})
	}
}

// The keys round-trip through the yaml loader and through ConfigToYaml.
func TestUnmarshalConfig_DutyOffsetsRoundTrip(t *testing.T) {
	yamlCfg := strings.Join([]string{
		"CONFIG_NAME: lightchain-offsets",
		"PRESET_BASE: mainnet",
		"SECONDS_PER_SLOT: 2",
		"SLOT_DURATION_MS: 2000",
		"ATTESTATION_OFFSET_MS: 1000",
		"AGGREGATION_OFFSET_MS: 1500",
	}, "\n")
	cfg, err := params.UnmarshalConfig([]byte(yamlCfg), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), cfg.AttestationOffsetMs)
	require.Equal(t, uint64(1500), cfg.AggregationOffsetMs)
	require.Equal(t, time.Second, cfg.AttestationDue(cfg.SlotDuration(), 0))
	require.Equal(t, 1500*time.Millisecond, cfg.AggregationDue(cfg.SlotDuration(), 0))

	// ConfigToYaml writes the keys back and they load again unchanged.
	again, err := params.UnmarshalConfig(params.ConfigToYaml(cfg), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), again.AttestationOffsetMs)
	require.Equal(t, uint64(1500), again.AggregationOffsetMs)

	// Absent keys: zero, legacy thirds.
	legacy, err := params.UnmarshalConfig([]byte("CONFIG_NAME: legacy\nSECONDS_PER_SLOT: 2\nSLOT_DURATION_MS: 2000\n"), nil)
	require.NoError(t, err)
	require.Equal(t, uint64(0), legacy.AttestationOffsetMs)
	require.Equal(t, uint64(0), legacy.AggregationOffsetMs)
	require.Equal(t, 666*time.Millisecond, legacy.AttestationDue(legacy.SlotDuration(), 0))
	require.Equal(t, 1333*time.Millisecond, legacy.AggregationDue(legacy.SlotDuration(), 0))
}

func TestUnmarshalConfig_DutyOffsetsRejected(t *testing.T) {
	yamlCfg := strings.Join([]string{
		"CONFIG_NAME: lightchain-bad-offsets",
		"SECONDS_PER_SLOT: 2",
		"SLOT_DURATION_MS: 2000",
		"ATTESTATION_OFFSET_MS: 1500",
		"AGGREGATION_OFFSET_MS: 1000",
	}, "\n")
	_, err := params.UnmarshalConfig([]byte(yamlCfg), nil)
	require.ErrorContains(t, "attestations must be due before aggregates", err)
}
