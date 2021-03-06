package featureconfig

import "github.com/urfave/cli/v2"

// Deprecated flags list.
const deprecatedUsage = "DEPRECATED. DO NOT USE."

var (
	// To deprecate a feature flag, first copy the example below, then insert deprecated flag in `deprecatedFlags`.
	exampleDeprecatedFeatureFlag = &cli.StringFlag{
		Name:   "name",
		Usage:  deprecatedUsage,
		Hidden: true,
	}
	deprecatedEnablePruningDepositProofs = &cli.BoolFlag{
		Name:   "enable-pruning-deposit-proofs",
		Usage:  deprecatedUsage,
		Hidden: true,
	}
	deprecatedEnableEth1DataMajorityVote = &cli.BoolFlag{
		Name:   "enable-eth1-data-majority-vote",
		Usage:  deprecatedUsage,
		Hidden: true,
	}
	deprecatedEnableBlst = &cli.BoolFlag{
		Name:   "blst",
		Usage:  deprecatedUsage,
		Hidden: true,
	}
)

var deprecatedFlags = []cli.Flag{
	exampleDeprecatedFeatureFlag,
	deprecatedEnablePruningDepositProofs,
	deprecatedEnableEth1DataMajorityVote,
	deprecatedEnableBlst,
}
