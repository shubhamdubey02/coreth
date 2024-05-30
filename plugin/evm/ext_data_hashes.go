package evm

import (
	_ "embed"
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"
)

var (
	//go:embed mustang_ext_data_hashes.json
	rawMustangExtDataHashes []byte
	mustangExtDataHashes    map[common.Hash]common.Hash

	//go:embed mainnet_ext_data_hashes.json
	rawMainnetExtDataHashes []byte
	mainnetExtDataHashes    map[common.Hash]common.Hash
)

func init() {
	if err := json.Unmarshal(rawMustangExtDataHashes, &mustangExtDataHashes); err != nil {
		panic(err)
	}
	rawMustangExtDataHashes = nil
	if err := json.Unmarshal(rawMainnetExtDataHashes, &mainnetExtDataHashes); err != nil {
		panic(err)
	}
	rawMainnetExtDataHashes = nil
}
