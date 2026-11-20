package features

// LightChain: this whole file is fork code: the --allow-duties-with-el-rejecting-head flag (audit S2 duty guard).

import (
	"flag"
	"testing"

	"github.com/OffchainLabs/prysm/v7/testing/assert"
	"github.com/OffchainLabs/prysm/v7/testing/require"
	"github.com/urfave/cli/v2"
)

func TestConfigureBeaconChain_AllowDutiesWithELRejectingHead(t *testing.T) {
	defer Init(&Flags{})

	// Default: flag absent, guard on.
	app := cli.App{}
	set := flag.NewFlagSet("test", 0)
	require.NoError(t, ConfigureBeaconChain(cli.NewContext(&app, set, nil)))
	assert.Equal(t, false, Get().AllowDutiesWithELRejectingHead)

	// Flag set: guard off.
	set = flag.NewFlagSet("test", 0)
	set.Bool(allowDutiesWithELRejectingHead.Name, true, "test")
	require.NoError(t, ConfigureBeaconChain(cli.NewContext(&app, set, nil)))
	assert.Equal(t, true, Get().AllowDutiesWithELRejectingHead)
}

func TestAllowDutiesWithELRejectingHead_IsABeaconChainFlag(t *testing.T) {
	found := false
	for _, f := range BeaconChainFlags {
		for _, n := range f.Names() {
			if n == "allow-duties-with-el-rejecting-head" {
				found = true
			}
		}
	}
	assert.Equal(t, true, found, "flag must be in BeaconChainFlags so the beacon node parses it and lists it in --help")
}
