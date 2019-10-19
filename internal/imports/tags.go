// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package imports

var tags map[string]bool

func Tags() map[string]bool {
	if tags == nil {
		tags = loadTags()
	}
	return tags
}

func loadTags() map[string]bool {
	return map[string]bool{}
}
