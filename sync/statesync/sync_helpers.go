// (c) 2021-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/shubhamdubey02/coreth/core/rawdb"
	"github.com/shubhamdubey02/corethreth/core/types"
	"github.com/shubhamdubey02/corethreth/trie"
)

// writeAccountSnapshot stores the account represented by [acc] to the snapshot at [accHash], using
// SlimAccountRLP format (omitting empty code/storage).
func writeAccountSnapshot(db ethdb.KeyValueWriter, accHash common.Hash, acc types.StateAccount) {
	slimAccount := types.SlimAccountRLP(acc)
	rawdb.WriteAccountSnapshot(db, accHash, slimAccount)
}

// writeAccountStorageSnapshotFromTrie iterates the trie at [storageTrie] and copies all entries
// to the storage snapshot for [accountHash].
func writeAccountStorageSnapshotFromTrie(batch ethdb.Batch, batchSize int, accountHash common.Hash, storageTrie *trie.Trie) error {
	nodeIt, err := storageTrie.NodeIterator(nil)
	if err != nil {
		return err
	}
	it := trie.NewIterator(nodeIt)
	for it.Next() {
		rawdb.WriteStorageSnapshot(batch, accountHash, common.BytesToHash(it.Key), it.Value)
		if batch.ValueSize() > batchSize {
			if err := batch.Write(); err != nil {
				return err
			}
			batch.Reset()
		}
	}
	if it.Err != nil {
		return it.Err
	}
	return batch.Write()
}
