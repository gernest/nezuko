// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// z mod graph

package modcmd

import (
	"bufio"
	"os"
	"sort"

	"github.com/gernest/nezuko/internal/base"
	"github.com/gernest/nezuko/internal/modload"
	"github.com/gernest/nezuko/internal/module"
	"github.com/gernest/nezuko/internal/par"
)

var cmdGraph = &base.Command{
	UsageLine: "z mod graph",
	Short:     "print module requirement graph",
	Long: `
Graph prints the module requirement graph (with replacements applied)
in text form. Each line in the output has two space-separated fields: a module
and one of its requirements. Each module is identified as a string of the form
path@version, except for the main module, which has no @version suffix.
	`,
	Run: runGraph,
}

func runGraph(cmd *base.Command, args []string) {
	if len(args) > 0 {
		base.Fatalf("z mod graph: graph takes no arguments")
	}
	modload.LoadBuildList()

	reqs := modload.MinReqs()
	format := func(m module.Version) string {
		if m.Version == "" {
			return m.Path
		}
		return m.Path + "@" + m.Version
	}

	// Note: using par.Work only to manage work queue.
	// No parallelism here, so no locking.
	var out []string
	var deps int // index in out where deps start
	var work par.Work
	work.Add(modload.Target)
	work.Do(1, func(item interface{}) {
		m := item.(module.Version)
		list, _ := reqs.Required(m)
		for _, r := range list {
			work.Add(r)
			out = append(out, format(m)+" "+format(r)+"\n")
		}
		if m == modload.Target {
			deps = len(out)
		}
	})

	sort.Slice(out[deps:], func(i, j int) bool {
		return out[deps+i][0] < out[deps+j][0]
	})

	w := bufio.NewWriter(os.Stdout)
	for _, line := range out {
		w.WriteString(line)
	}
	w.Flush()
}
