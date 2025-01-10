// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shubhamdubey02/cryftgo/utils/logging"
	"github.com/shubhamdubey02/cryftgo/utils/ulimit"
	"github.com/shubhamdubey02/cryftgo/vms/rpcchainvm"

	"github.com/shubhamdubey02/coreth/plugin/evm"
)

func main() {
	version, err := PrintVersion()
	if err != nil {
		fmt.Printf("couldn't get config: %s", err)
		os.Exit(1)
	}
	if version {
		fmt.Println(evm.Version)
		os.Exit(0)
	}
	if err := ulimit.Set(ulimit.DefaultFDLimit, logging.NoLog{}); err != nil {
		fmt.Printf("failed to set fd limit correctly due to: %s", err)
		os.Exit(1)
	}

	rpcchainvm.Serve(context.Background(), &evm.VM{IsPlugin: true})
}
