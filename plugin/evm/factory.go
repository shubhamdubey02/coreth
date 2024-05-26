// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evm

import (
	"github.com/cryft-labs/cryftgo/ids"
	"github.com/cryft-labs/cryftgo/utils/logging"
	"github.com/cryft-labs/cryftgo/vms"
)

var (
	// ID this VM should be referenced by
	ID = ids.ID{'e', 'v', 'm'}

	_ vms.Factory = &Factory{}
)

type Factory struct{}

func (*Factory) New(logging.Logger) (interface{}, error) {
	return &VM{}, nil
}
