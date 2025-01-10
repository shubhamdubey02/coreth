// (c) 2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shubhamdubey02/coreth/core/rawdb"
	"github.com/shubhamdubey02/corethreth/precompile/contract"
	"github.com/stretchr/testify/require"
)

func NewTestStateDB(t testing.TB) contract.StateDB {
	db := rawdb.NewMemoryDatabase()
	stateDB, err := New(common.Hash{}, NewDatabase(db), nil)
	require.NoError(t, err)
	return stateDB
}
