// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/gernest/nezuko/internal/base"
	"github.com/gernest/nezuko/internal/cache"
	"github.com/gernest/nezuko/internal/cfg"
	"github.com/gernest/nezuko/internal/load"
	"github.com/gernest/nezuko/internal/modfetch"
	"github.com/gernest/nezuko/internal/modfetch/codehost"
	"github.com/gernest/nezuko/internal/modfile"
	"github.com/gernest/nezuko/internal/module"
	"github.com/gernest/nezuko/internal/mvs"
	"github.com/gernest/nezuko/internal/renameio"
	"github.com/gernest/nezuko/internal/search"
)

var (
	cwd         string // TODO(bcmills): Is this redundant with base.Cwd?
	initialized bool

	modRoot     string
	modFile     *modfile.File
	modFileData []byte
	excluded    map[module.Version]bool
	Target      module.Version

	gopath string

	CmdModInit   bool   // running 'go mod init'
	CmdModModule string // module argument for 'go mod init'
)

// ModFile returns the parsed z.mod file.
//
// Note that after calling ImportPaths or LoadBuildList,
// the require statements in the modfile.File are no longer
// the source of truth and will be ignored: edits made directly
// will be lost at the next call to WriteGoMod.
// To make permanent changes to the require statements
// in z.mod, edit it before calling ImportPaths or LoadBuildList.
func ModFile() *modfile.File {
	Init()
	if modFile == nil {
		die()
	}
	return modFile
}

func BinDir() string {
	Init()
	return filepath.Join(gopath, "bin")
}

// mustUseModules reports whether we are invoked as vgo
// (as opposed to go).
// If so, we only support builds with z.mod files.
func mustUseModules() bool {
	name := os.Args[0]
	name = name[strings.LastIndex(name, "/")+1:]
	name = name[strings.LastIndex(name, `\`)+1:]
	return strings.HasPrefix(name, "vgo")
}

// Init determines whether module mode is enabled, locates the root of the
// current module (if any), sets environment variables for Git subprocesses, and
// configures the cfg, codehost, load, modfetch, and search packages for use
// with modules.
func Init() {
	if initialized {
		return
	}
	initialized = true

	// Disable any prompting for passwords by Git.
	// Only has an effect for 2.3.0 or later, but avoiding
	// the prompt in earlier versions is just too hard.
	// If user has explicitly set GIT_TERMINAL_PROMPT=1, keep
	// prompting.
	// See golang.org/issue/9341 and golang.org/issue/12706.
	if os.Getenv("GIT_TERMINAL_PROMPT") == "" {
		os.Setenv("GIT_TERMINAL_PROMPT", "0")
	}

	// Disable any ssh connection pooling by Git.
	// If a Git subprocess forks a child into the background to cache a new connection,
	// that child keeps stdout/stderr open. After the Git subprocess exits,
	// os /exec expects to be able to read from the stdout/stderr pipe
	// until EOF to get all the data that the Git subprocess wrote before exiting.
	// The EOF doesn't come until the child exits too, because the child
	// is holding the write end of the pipe.
	// This is unfortunate, but it has come up at least twice
	// (see golang.org/issue/13453 and golang.org/issue/16104)
	// and confuses users when it does.
	// If the user has explicitly set GIT_SSH or GIT_SSH_COMMAND,
	// assume they know what they are doing and don't step on it.
	// But default to turning off ControlMaster.
	if os.Getenv("GIT_SSH") == "" && os.Getenv("GIT_SSH_COMMAND") == "" {
		os.Setenv("GIT_SSH_COMMAND", "ssh -o ControlMaster=no")
	}

	var err error
	cwd, err = os.Getwd()
	if err != nil {
		base.Fatalf("z: %v", err)
	}

	if CmdModInit {
		// Running 'z mod init': z.mod will be created in current directory.
		modRoot = cwd
	} else {
		modRoot, _ = FindModuleRoot(cwd, "", true)
		if modRoot == "" {
		} else if search.InDir(modRoot, os.TempDir()) == "." {
			// If you create /tmp/z.mod for experimenting,
			// then any tests that create work directories under /tmp
			// will find it and get modules when they're not expecting them.
			// It's a bit of a peculiar thing to disallow but quite mysterious
			// when it happens. See golang.org/issue/26708.
			modRoot = ""
			fmt.Fprintf(os.Stderr, "z: warning: ignoring z.mod in system temp root %v\n", os.TempDir())
		}
	}

	// We're in module mode. Install the hooks to make it work.

	if c := cache.Default(); c == nil {
		// With modules, there are no install locations for packages
		// other than the build cache.
		base.Fatalf("z: cannot use modules with build cache disabled")
	}

	list := filepath.SplitList(cfg.BuildContext.ZIGPATH)
	if len(list) == 0 || list[0] == "" {
		base.Fatalf("missing $ZIGPATH")
	}
	gopath = list[0]
	if _, err := os.Stat(filepath.Join(gopath, "z.mod")); err == nil {
		base.Fatalf("$GOPATH/z.mod exists but should not")
	}

	oldSrcMod := filepath.Join(list[0], "src/mod")
	pkgMod := filepath.Join(list[0], "pkg/mod")
	infoOld, errOld := os.Stat(oldSrcMod)
	_, errMod := os.Stat(pkgMod)
	if errOld == nil && infoOld.IsDir() && errMod != nil && os.IsNotExist(errMod) {
		os.Rename(oldSrcMod, pkgMod)
	}

	modfetch.PkgMod = pkgMod
	codehost.WorkRoot = filepath.Join(pkgMod, "cache/vcs")

	cfg.ModulesEnabled = true
	load.ModBinDir = BinDir
	load.ModLookup = Lookup
	load.ModPackageModuleInfo = PackageModuleInfo
	load.ModImportPaths = ImportPaths
	load.ModPackageBuildInfo = PackageBuildInfo
	load.ModInfoProg = ModInfoProg
	load.ModImportFromFiles = ImportFromFiles
	load.ModDirImportPath = DirImportPath

	if modRoot == "" {
		// We're in module mode, but not inside a module.
		//
		// If the command is 'go get' or 'go list' and all of the args are in the
		// same existing module, we could use that module's download directory in
		// the module cache as the module root, applying any replacements and/or
		// exclusions specified by that module. However, that would leave us in a
		// strange state: we want 'go get' to be consistent with 'go list', and 'go
		// list' should be able to operate on multiple modules. Moreover, the 'get'
		// target might specify relative file paths (e.g. in the same repository) as
		// replacements, and we would not be able to apply those anyway: we would
		// need to either error out or ignore just those replacements, when a build
		// from an empty module could proceed without error.
		//
		// Instead, we'll operate as though we're in some ephemeral external module,
		// ignoring all replacements and exclusions uniformly.

		// Normally we check sums using the go.sum file from the main module, but
		// without a main module we do not have an authoritative go.sum file.
		//
		// TODO(bcmills): In Go 1.13, check sums when outside the main module.
		//
		// One possible approach is to merge the go.sum files from all of the
		// modules we download: that doesn't protect us against bad top-level
		// modules, but it at least ensures consistency for transitive dependencies.
	} else {
		modfetch.ZigSumFile = filepath.Join(modRoot, "z.sum")
		search.SetModRoot(modRoot)
	}
}

func init() {
	load.ModInit = Init

	// Set modfetch.PkgMod unconditionally, so that go clean -modcache can run even without modules enabled.
	if list := filepath.SplitList(cfg.BuildContext.ZIGPATH); len(list) > 0 && list[0] != "" {
		modfetch.PkgMod = filepath.Join(list[0], "pkg/mod")
	}
}

// ModRoot returns the root of the main module.
// It calls base.Fatalf if there is no main module.
func ModRoot() string {
	if !HasModRoot() {
		die()
	}
	return modRoot
}

// HasModRoot reports whether a main module is present.
// HasModRoot may return false even if Enabled returns true: for example, 'get'
// does not require a main module.
func HasModRoot() bool {
	Init()
	return modRoot != ""
}

// printStackInDie causes die to print a stack trace.
//
// It is enabled by the testgo tag, and helps to diagnose paths that
// unexpectedly require a main module.
var printStackInDie = false

func die() {
	if printStackInDie {
		debug.PrintStack()
	}
	base.Fatalf("z: cannot find main module; see 'z help modules'")
}

// InitMod sets Target and, if there is a main module, parses the initial build
// list from its z.mod file, creating and populating that file if needed.
func InitMod() {
	if len(buildList) > 0 {
		return
	}

	Init()
	if modRoot == "" {
		Target = module.Version{Path: "command-line-arguments"}
		buildList = []module.Version{Target}
		return
	}

	if CmdModInit {
		// Running go mod init: do legacy module conversion
		legacyModInit()
		modFileToBuildList()
		WriteGoMod()
		return
	}

	gomod := filepath.Join(modRoot, "z.mod")
	data, err := ioutil.ReadFile(gomod)
	if err != nil {
		if os.IsNotExist(err) {
			legacyModInit()
			modFileToBuildList()
			WriteGoMod()
			return
		}
		base.Fatalf("z: %v", err)
	}

	f, err := modfile.Parse(gomod, data, fixVersion)
	if err != nil {
		// Errors returned by modfile.Parse begin with file:line.
		base.Fatalf("z: errors parsing z.mod:\n%s\n", err)
	}
	modFile = f
	modFileData = data

	if len(f.Syntax.Stmt) == 0 || f.Module == nil {
		// Empty mod file. Must add module path.
		path, err := FindModulePath(modRoot)
		if err != nil {
			base.Fatalf("z: %v", err)
		}
		f.AddModuleStmt(path)
	}

	if len(f.Syntax.Stmt) == 1 && f.Module != nil {
		// Entire file is just a module statement.
		// Populate require if possible.
		legacyModInit()
	}

	excluded = make(map[module.Version]bool)
	for _, x := range f.Exclude {
		excluded[x.Mod] = true
	}
	modFileToBuildList()
	WriteGoMod()
}

// modFileToBuildList initializes buildList from the modFile.
func modFileToBuildList() {
	Target = modFile.Module.Mod
	list := []module.Version{Target}
	for _, r := range modFile.Require {
		list = append(list, r.Mod)
	}
	buildList = list
}

// Allowed reports whether module m is allowed (not excluded) by the main module's z.mod.
func Allowed(m module.Version) bool {
	return !excluded[m]
}

func legacyModInit() {
	if modFile == nil {
		path, err := FindModulePath(modRoot)
		if err != nil {
			base.Fatalf("z: %v", err)
		}
		fmt.Fprintf(os.Stderr, "z: creating new z.mod: module %s\n", path)
		modFile = new(modfile.File)
		modFile.AddModuleStmt(path)
	}

	addGoStmt()
}

// InitGoStmt adds a go statement, unless there already is one.
func InitGoStmt() {
	if modFile.Go == nil {
		addGoStmt()
	}
}

// addGoStmt adds a go statement referring to the current version.
func addGoStmt() {
	tags := build.Default.ReleaseTags
	version := tags[len(tags)-1]
	if !strings.HasPrefix(version, "go") || !modfile.GoVersionRE.MatchString(version[2:]) {
		base.Fatalf("z: unrecognized default version %q", version)
	}
	if err := modFile.AddGoStmt(version[2:]); err != nil {
		base.Fatalf("z: internal error: %v", err)
	}
}

// Exported only for testing.
func FindModuleRoot(dir, limit string, legacyConfigOK bool) (root, file string) {
	dir = filepath.Clean(dir)
	limit = filepath.Clean(limit)

	// Look for enclosing z.mod.
	for {
		if fi, err := os.Stat(filepath.Join(dir, "z.mod")); err == nil && !fi.IsDir() {
			return dir, "z.mod"
		}
		if dir == limit {
			break
		}
		d := filepath.Dir(dir)
		if d == dir {
			break
		}
		dir = d
	}
	return "", ""
}

// Exported only for testing.
func FindModulePath(dir string) (string, error) {
	if CmdModModule != "" {
		// Running go mod init x/y/z; return x/y/z.
		return CmdModModule, nil
	}

	// Cast about for import comments,
	// first in top-level directory, then in subdirectories.
	list, _ := ioutil.ReadDir(dir)
	for _, info := range list {
		if info.Mode().IsRegular() && strings.HasSuffix(info.Name(), ".go") {
			if com := findImportComment(filepath.Join(dir, info.Name())); com != "" {
				return com, nil
			}
		}
	}
	for _, info1 := range list {
		if info1.IsDir() {
			files, _ := ioutil.ReadDir(filepath.Join(dir, info1.Name()))
			for _, info2 := range files {
				if info2.Mode().IsRegular() && strings.HasSuffix(info2.Name(), ".go") {
					if com := findImportComment(filepath.Join(dir, info1.Name(), info2.Name())); com != "" {
						return path.Dir(com), nil
					}
				}
			}
		}
	}

	// Look for Godeps.json declaring import path.
	data, _ := ioutil.ReadFile(filepath.Join(dir, "Godeps/Godeps.json"))
	var cfg1 struct{ ImportPath string }
	json.Unmarshal(data, &cfg1)
	if cfg1.ImportPath != "" {
		return cfg1.ImportPath, nil
	}

	// Look for vendor.json declaring import path.
	data, _ = ioutil.ReadFile(filepath.Join(dir, "vendor/vendor.json"))
	var cfg2 struct{ RootPath string }
	json.Unmarshal(data, &cfg2)
	if cfg2.RootPath != "" {
		return cfg2.RootPath, nil
	}

	// Look for path in GOPATH.
	for _, gpdir := range filepath.SplitList(cfg.BuildContext.ZIGPATH) {
		if gpdir == "" {
			continue
		}
		if rel := search.InDir(dir, filepath.Join(gpdir, "src")); rel != "" && rel != "." {
			return filepath.ToSlash(rel), nil
		}
	}

	// Look for .git/config with github origin as last resort.
	data, _ = ioutil.ReadFile(filepath.Join(dir, ".git/config"))
	if m := gitOriginRE.FindSubmatch(data); m != nil {
		return "github.com/" + string(m[1]), nil
	}

	return "", fmt.Errorf("cannot determine module path for source directory %s (outside GOPATH, no import comments)", dir)
}

var (
	gitOriginRE     = regexp.MustCompile(`(?m)^\[remote "origin"\]\r?\n\turl = (?:https://github.com/|git@github.com:|gh:)([^/]+/[^/]+?)(\.git)?\r?\n`)
	importCommentRE = regexp.MustCompile(`(?m)^package[ \t]+[^ \t\r\n/]+[ \t]+//[ \t]+import[ \t]+(\"[^"]+\")[ \t]*\r?\n`)
)

func findImportComment(file string) string {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return ""
	}
	m := importCommentRE.FindSubmatch(data)
	if m == nil {
		return ""
	}
	path, err := strconv.Unquote(string(m[1]))
	if err != nil {
		return ""
	}
	return path
}

var allowWriteGoMod = true

// DisallowWriteGoMod causes future calls to WriteGoMod to do nothing at all.
func DisallowWriteGoMod() {
	allowWriteGoMod = false
}

// AllowWriteGoMod undoes the effect of DisallowWriteGoMod:
// future calls to WriteGoMod will update z.mod if needed.
// Note that any past calls have been discarded, so typically
// a call to AlowWriteGoMod should be followed by a call to WriteGoMod.
func AllowWriteGoMod() {
	allowWriteGoMod = true
}

// MinReqs returns a Reqs with minimal dependencies of Target,
// as will be written to z.mod.
func MinReqs() mvs.Reqs {
	var direct []string
	for _, m := range buildList[1:] {
		if loaded.direct[m.Path] {
			direct = append(direct, m.Path)
		}
	}
	min, err := mvs.Req(Target, buildList, direct, Reqs())
	if err != nil {
		base.Fatalf("z: %v", err)
	}
	return &mvsReqs{buildList: append([]module.Version{Target}, min...)}
}

// WriteGoMod writes the current build list back to z.mod.
func WriteGoMod() {
	// If we're using -mod=vendor we basically ignored
	// z.mod, so definitely don't try to write back our
	// incomplete view of the world.
	if !allowWriteGoMod || cfg.BuildMod == "vendor" {
		return
	}

	// If we aren't in a module, we don't have anywhere to write a z.mod file.
	if modRoot == "" {
		return
	}

	if loaded != nil {
		reqs := MinReqs()
		min, err := reqs.Required(Target)
		if err != nil {
			base.Fatalf("z: %v", err)
		}
		var list []*modfile.Require
		for _, m := range min {
			list = append(list, &modfile.Require{
				Mod:      m,
				Indirect: !loaded.direct[m.Path],
			})
		}
		modFile.SetRequire(list)
	}

	modFile.Cleanup() // clean file after edits
	new, err := modFile.Format()
	if err != nil {
		base.Fatalf("z: %v", err)
	}

	// Always update go.sum, even if we didn't change z.mod: we may have
	// downloaded modules that we didn't have before.
	modfetch.WriteZigSum()

	if bytes.Equal(new, modFileData) {
		// We don't need to modify z.mod from what we read previously.
		// Ignore any intervening edits.
		return
	}
	if cfg.BuildMod == "readonly" {
		base.Fatalf("z: updates to z.mod needed, disabled by -mod=readonly")
	}

	unlock := modfetch.SideLock()
	defer unlock()

	file := filepath.Join(modRoot, "z.mod")
	old, err := ioutil.ReadFile(file)
	if !bytes.Equal(old, modFileData) {
		if bytes.Equal(old, new) {
			// Some other process wrote the same z.mod file that we were about to write.
			modFileData = new
			return
		}
		if err != nil {
			base.Fatalf("z: can't determine whether z.mod has changed: %v", err)
		}
		// The contents of the z.mod file have changed. In theory we could add all
		// of the new modules to the build list, recompute, and check whether any
		// module in *our* build list got bumped to a different version, but that's
		// a lot of work for marginal benefit. Instead, fail the command: if users
		// want to run concurrent commands, they need to start with a complete,
		// consistent module definition.
		base.Fatalf("z: updates to z.mod needed, but contents have changed")

	}

	if err := renameio.WriteFile(file, new); err != nil {
		base.Fatalf("error writing z.mod: %v", err)
	}
	modFileData = new
}

func fixVersion(path, vers string) (string, error) {
	// Special case: remove the old -gopkgin- hack.
	if strings.HasPrefix(path, "gopkg.in/") && strings.Contains(vers, "-gopkgin-") {
		vers = vers[strings.Index(vers, "-gopkgin-")+len("-gopkgin-"):]
	}

	// fixVersion is called speculatively on every
	// module, version pair from every z.mod file.
	// Avoid the query if it looks OK.
	_, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("malformed module path: %s", path)
	}
	if vers != "" && module.CanonicalVersion(vers) == vers && module.MatchPathMajor(vers, pathMajor) {
		return vers, nil
	}

	info, err := Query(path, vers, nil)
	if err != nil {
		return "", err
	}
	return info.Version, nil
}
