// (c) 2019-2020, Ava Labs, Inc.
//
// This file is a derived work, based on the go-ethereum library whose original
// notices appear below.
//
// It is distributed under a license compatible with the licensing terms of the
// original code from which it is derived.
//
// Much love to the original authors for their work.
// **********
// Copyright 2016 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common/compiler"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/shubhamdubey02/coreth/accounts/abi/bind"
	"github.com/shubhamdubey02/corethreth/cmd/utils"
	"github.com/shubhamdubey02/corethreth/internal/flags"
	"github.com/urfave/cli/v2"
)

var (
	// Flags needed by abigen
	abiFlag = &cli.StringFlag{
		Name:  "abi",
		Usage: "Path to the Ethereum contract ABI json to bind, - for STDIN",
	}
	binFlag = &cli.StringFlag{
		Name:  "bin",
		Usage: "Path to the Ethereum contract bytecode (generate deploy method)",
	}
	typeFlag = &cli.StringFlag{
		Name:  "type",
		Usage: "Struct name for the binding (default = package name)",
	}
	jsonFlag = &cli.StringFlag{
		Name:  "combined-json",
		Usage: "Path to the combined-json file generated by compiler, - for STDIN",
	}
	excFlag = &cli.StringFlag{
		Name:  "exc",
		Usage: "Comma separated types to exclude from binding",
	}
	pkgFlag = &cli.StringFlag{
		Name:  "pkg",
		Usage: "Package name to generate the binding into",
	}
	outFlag = &cli.StringFlag{
		Name:  "out",
		Usage: "Output file for the generated binding (default = stdout)",
	}
	langFlag = &cli.StringFlag{
		Name:  "lang",
		Usage: "Destination language for the bindings (go)",
		Value: "go",
	}
	aliasFlag = &cli.StringFlag{
		Name:  "alias",
		Usage: "Comma separated aliases for function and event renaming, e.g. original1=alias1, original2=alias2",
	}
)

var app = flags.NewApp("Ethereum ABI wrapper code generator")

func init() {
	app.Name = "abigen"
	app.Flags = []cli.Flag{
		abiFlag,
		binFlag,
		typeFlag,
		jsonFlag,
		excFlag,
		pkgFlag,
		outFlag,
		langFlag,
		aliasFlag,
	}
	app.Action = abigen
}

func abigen(c *cli.Context) error {
	utils.CheckExclusive(c, abiFlag, jsonFlag) // Only one source can be selected.

	if c.String(pkgFlag.Name) == "" {
		utils.Fatalf("No destination package specified (--pkg)")
	}
	var lang bind.Lang
	switch c.String(langFlag.Name) {
	case "go":
		lang = bind.LangGo
	default:
		utils.Fatalf("Unsupported destination language \"%s\" (--lang)", c.String(langFlag.Name))
	}
	// If the entire solidity code was specified, build and bind based on that
	var (
		abis    []string
		bins    []string
		types   []string
		sigs    []map[string]string
		libs    = make(map[string]string)
		aliases = make(map[string]string)
	)
	if c.String(abiFlag.Name) != "" {
		// Load up the ABI, optional bytecode and type name from the parameters
		var (
			abi []byte
			err error
		)
		input := c.String(abiFlag.Name)
		if input == "-" {
			abi, err = io.ReadAll(os.Stdin)
		} else {
			abi, err = os.ReadFile(input)
		}
		if err != nil {
			utils.Fatalf("Failed to read input ABI: %v", err)
		}
		abis = append(abis, string(abi))

		var bin []byte
		if binFile := c.String(binFlag.Name); binFile != "" {
			if bin, err = os.ReadFile(binFile); err != nil {
				utils.Fatalf("Failed to read input bytecode: %v", err)
			}
			if strings.Contains(string(bin), "//") {
				utils.Fatalf("Contract has additional library references, please use other mode(e.g. --combined-json) to catch library infos")
			}
		}
		bins = append(bins, string(bin))

		kind := c.String(typeFlag.Name)
		if kind == "" {
			kind = c.String(pkgFlag.Name)
		}
		types = append(types, kind)
	} else {
		// Generate the list of types to exclude from binding
		var exclude *nameFilter
		if c.IsSet(excFlag.Name) {
			var err error
			if exclude, err = newNameFilter(strings.Split(c.String(excFlag.Name), ",")...); err != nil {
				utils.Fatalf("Failed to parse excludes: %v", err)
			}
		}
		var contracts map[string]*compiler.Contract

		if c.IsSet(jsonFlag.Name) {
			var (
				input      = c.String(jsonFlag.Name)
				jsonOutput []byte
				err        error
			)
			if input == "-" {
				jsonOutput, err = io.ReadAll(os.Stdin)
			} else {
				jsonOutput, err = os.ReadFile(input)
			}
			if err != nil {
				utils.Fatalf("Failed to read combined-json: %v", err)
			}
			contracts, err = compiler.ParseCombinedJSON(jsonOutput, "", "", "", "")
			if err != nil {
				utils.Fatalf("Failed to read contract information from json output: %v", err)
			}
		}
		// Gather all non-excluded contract for binding
		for name, contract := range contracts {
			// fully qualified name is of the form <solFilePath>:<type>
			nameParts := strings.Split(name, ":")
			typeName := nameParts[len(nameParts)-1]
			if exclude != nil && exclude.Matches(name) {
				fmt.Fprintf(os.Stderr, "excluding: %v\n", name)
				continue
			}
			abi, err := json.Marshal(contract.Info.AbiDefinition) // Flatten the compiler parse
			if err != nil {
				utils.Fatalf("Failed to parse ABIs from compiler output: %v", err)
			}
			abis = append(abis, string(abi))
			bins = append(bins, contract.Code)
			sigs = append(sigs, contract.Hashes)
			types = append(types, typeName)

			// Derive the library placeholder which is a 34 character prefix of the
			// hex encoding of the keccak256 hash of the fully qualified library name.
			// Note that the fully qualified library name is the path of its source
			// file and the library name separated by ":".
			libPattern := crypto.Keccak256Hash([]byte(name)).String()[2:36] // the first 2 chars are 0x
			libs[libPattern] = typeName
		}
	}
	// Extract all aliases from the flags
	if c.IsSet(aliasFlag.Name) {
		// We support multi-versions for aliasing
		// e.g.
		//      foo=bar,foo2=bar2
		//      foo:bar,foo2:bar2
		re := regexp.MustCompile(`(?:(\w+)[:=](\w+))`)
		submatches := re.FindAllStringSubmatch(c.String(aliasFlag.Name), -1)
		for _, match := range submatches {
			aliases[match[1]] = match[2]
		}
	}
	// Generate the contract binding
	code, err := bind.Bind(types, abis, bins, sigs, c.String(pkgFlag.Name), lang, libs, aliases)
	if err != nil {
		utils.Fatalf("Failed to generate ABI binding: %v", err)
	}
	// Either flush it out to a file or display on the standard output
	if !c.IsSet(outFlag.Name) {
		fmt.Printf("%s\n", code)
		return nil
	}
	if err := os.WriteFile(c.String(outFlag.Name), []byte(code), 0600); err != nil {
		utils.Fatalf("Failed to write ABI binding: %v", err)
	}
	return nil
}

func main() {
	log.SetDefault(log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr, log.LevelInfo, true)))

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
