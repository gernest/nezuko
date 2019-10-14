// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package envcmd implements the ``go env'' command.
package envcmd

import (
	"github.com/gernest/nezuko/internal/cfg"
)

func MkEnv() []cfg.EnvVar {
	return []cfg.EnvVar{
		{Name: "ZIGPATH", Value: cfg.ZigPath},
	}
}
