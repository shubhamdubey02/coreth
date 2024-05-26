// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"github.com/cryft-labs/cryftgo/api/metrics"
	"github.com/cryft-labs/cryftgo/ids"
	"github.com/cryft-labs/cryftgo/snow"
	"github.com/cryft-labs/cryftgo/snow/validators"
	"github.com/cryft-labs/cryftgo/utils/crypto/bls"
	"github.com/cryft-labs/cryftgo/utils/logging"
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
