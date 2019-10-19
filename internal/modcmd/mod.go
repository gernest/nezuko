// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package modcmd implements the ``go mod'' command.
package modcmd

import "github.com/gernest/nezuko/internal/base"

var CmdMod = &base.Command{
	UsageLine: "z mod",
	Short:     "module maintenance",
	Long:      `Z mod provides access to operations on modules.`,

	Commands: []*base.Command{
		cmdDownload,
		cmdEdit,
		cmdGraph,
		cmdInit,
		cmdTidy,
		cmdVendor,
		cmdVerify,
		cmdWhy,
	},
}
