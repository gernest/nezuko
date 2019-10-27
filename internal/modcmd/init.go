// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// z mod init

package modcmd

import (
	"os"
	"strings"

	"github.com/gernest/nezuko/internal/base"
	"github.com/gernest/nezuko/internal/modload"
)

var cmdInit = &base.Command{
	UsageLine: "z mod init [module]",
	Short:     "initialize new module in current directory",
	Long: `
Init initializes and writes a new z.mod to the current directory,
in effect creating a new module rooted at the current directory.
The file z.mod must not already exist.
	`,
	Run: runInit,
}

func runInit(cmd *base.Command, args []string) {
	modload.CmdModInit = true
	if len(args) > 1 {
		base.Fatalf("z mod init: too many arguments")
	}
	if len(args) == 1 {
		modload.CmdModModule = args[0]
	}
	if _, err := os.Stat("z.mod"); err == nil {
		base.Fatalf("z mod init: z.mod already exists")
	}
	if strings.Contains(modload.CmdModModule, "@") {
		base.Fatalf("z mod init: module path must not contain '@'")
	}
	modload.InitMod() // does all the hard work
}
