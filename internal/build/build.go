package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var Default Context = defaultContext()

type Context struct {
	ZIGPATH string // Go path

	JoinPath func(elem ...string) string

	// SplitPathList splits the path list into a slice of individual paths.
	// If SplitPathList is nil, Import uses filepath.SplitList.
	SplitPathList func(list string) []string

	// IsAbsPath reports whether path is an absolute path.
	// If IsAbsPath is nil, Import uses filepath.IsAbs.
	IsAbsPath func(path string) bool

	// IsDir reports whether the path names a directory.
	// If IsDir is nil, Import calls os.Stat and uses the result's IsDir method.
	IsDir func(path string) bool

	// HasSubdir reports whether dir is lexically a subdirectory of
	// root, perhaps multiple levels below. It does not try to check
	// whether dir exists.
	// If so, HasSubdir sets rel to a slash-separated path that
	// can be joined to root to produce a path equivalent to dir.
	// If HasSubdir is nil, Import uses an implementation built on
	// filepath.EvalSymlinks.
	HasSubdir func(root, dir string) (rel string, ok bool)

	// ReadDir returns a slice of os.FileInfo, sorted by Name,
	// describing the content of the named directory.
	// If ReadDir is nil, Import uses ioutil.ReadDir.
	ReadDir func(dir string) ([]os.FileInfo, error)

	// OpenFile opens a file (not a directory) for reading.
	// If OpenFile is nil, Import uses os.Open.
	OpenFile func(path string) (io.ReadCloser, error)
}

func envOr(name, def string) string {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	return s
}

func defaultGOPATH() string {
	env := "HOME"
	if runtime.GOOS == "windows" {
		env = "USERPROFILE"
	} else if runtime.GOOS == "plan9" {
		env = "home"
	}
	if home := os.Getenv(env); home != "" {
		def := filepath.Join(home, "zig")
		return def
	}
	return ""
}

func defaultContext() Context {
	var c Context
	c.ZIGPATH = envOr("ZIGPATH", defaultGOPATH())
	return c
}

func (ctx Context) ImportDir(path string, mode int) (*Package, error) {
	return nil, nil
}

// A Package describes the Go package found in a directory.
type Package struct {
	Dir           string // directory containing package sources
	Name          string // package name
	ImportComment string // path in import comment on package statement
	Doc           string // documentation synopsis
	ImportPath    string // import path of package ("" if unknown)
	Root          string // root of Go tree where this package lives
	SrcRoot       string // package source root directory ("" if unknown)
	PkgRoot       string // package install root directory ("" if unknown)

	// Source files
	GoFiles []string // .go source files (excluding CgoFiles, TestGoFiles, XTestGoFiles)

	// Dependency information
	Imports []string // import paths from GoFiles, CgoFiles
}

// IsLocalImport reports whether the import path is
// a local import path, like ".", "..", "./foo", or "../foo".
func IsLocalImport(path string) bool {
	return path == "." || path == ".." ||
		strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

func (ctxt *Context) Import(path string, srcDir string) (*Package, error) {
	p := &Package{
		ImportPath: path,
	}
	if path == "" {
		return p, fmt.Errorf("import %q: invalid import path", path)
	}
	return p, nil
}
