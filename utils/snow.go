// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"github.com/shubhamdubey02/cryftgo/api/metrics"
	"github.com/shubhamdubey02/cryftgoft/ids"
	"github.com/shubhamdubey02/cryftgoft/snow"
	"github.com/shubhamdubey02/cryftgoft/snow/validators"
	"github.com/shubhamdubey02/cryftgoft/utils/crypto/bls"
	"github.com/shubhamdubey02/cryftgoft/utils/logging"
)

func TestSnowContext() *snow.Context {
	sk, err := bls.NewSecretKey()
	if err != nil {
		panic(err)
	}
	pk := bls.PublicFromSecretKey(sk)
	return &snow.Context{
		NetworkID:      0,
		SubnetID:       ids.Empty,
		ChainID:        ids.Empty,
		NodeID:         ids.EmptyNodeID,
		PublicKey:      pk,
		Log:            logging.NoLog{},
		BCLookup:       ids.NewAliaser(),
		Metrics:        metrics.NewOptionalGatherer(),
		ChainDataDir:   "",
		ValidatorState: &validators.TestState{},
	}
}
