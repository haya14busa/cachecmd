package main

import (
	"context"
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const version = "v0.9.0"

// Update it when cache structure changed.
const cacheStructureVersion = "1"

const usageMessage = `Usage:	cachecmd [flags] {command}
	cachecmd runs a given command and caches the result of the command.
	Return cached result instead if cache found.`

const usageExample = `Example:
	$ cachecmd -ttl=10s date +%S
	14 # First run
	$ sleep 5s
	$ cachecmd -ttl=10s date +%S
	14 # Read from cache
	$ sleep 5s
	$ cachecmd -ttl=10s date +%S
	24 # cache is expired. Run command again and update cache.

	# Force update: set -ttl=0
	$ cachecmd -ttl=0 date +%S

	# TTL is 10 min. Return cache result immediately from cache and update cache
	# in background for every run.
	$ cachecmd -ttl=10m -async sh -c 'date +%s; sleep 3s

	# Cache result by current directory.
	$ cachecmd -ttl=10m -key="$(pwd)" go list ./...
	# https://github.com/github/hub
	$ cachecmd -ttl=10m -key="$(pwd)" -async hub issue`

func usage() {
	fmt.Fprintln(os.Stderr, usageMessage)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, usageExample)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "URL: https://github.com/haya14busa/cachecmd")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Version: %v\n", version)
}

type option struct {
	version  bool
	ttl      time.Duration
	async    bool
	cacheDir string
	cacheKey string
}

var flagOpt = &option{}

func init() {
	flag.BoolVar(&flagOpt.version, "version", false, "print version")
	flag.DurationVar(&flagOpt.ttl, "ttl", time.Minute, "TTL(Time to live) of cache")
	flag.BoolVar(&flagOpt.async, "async", false,
		"return result from cache immediately and update cache in background")
	flag.StringVar(&flagOpt.cacheDir, "cache_dir", cacheDir(), "cache directory.")
	flag.StringVar(&flagOpt.cacheKey, "key", "", "cache key in addition to given commands.")
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flagOpt.version {
		fmt.Fprintln(os.Stderr, version)
		return
	}
	code, err := run(os.Stdin, os.Stdout, os.Stderr, *flagOpt, flag.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cachecmd: %v\n", err)
	}
	os.Exit(code)
}

func run(r io.Reader, stdout, stderr io.Writer, opt option, command []string) (int, error) {
	if len(command) == 0 {
		usage()
		os.Exit(2)
	}
	cachecmd := CacheCmd{
		stdout:  stdout,
		stderr:  stderr,
		cmdName: command[0],
		cmdArgs: command[1:],
		opt:     opt,
	}
	return cachecmd.Run(context.Background())
}

type CacheCmd struct {
	stdout  io.Writer
	stderr  io.Writer
	cmdName string
	cmdArgs []string
	opt     option

	currentTime  time.Time
	cachecmdExec string
}

func (c *CacheCmd) Run(ctx context.Context) (exitcode int, err error) {
	code, err := c.fromCacheOrRun(ctx)
	if err != nil && code == 0 {
		code = 1
	}
	return code, err
}

// It may return exit code 0 as zero-value.
func (c *CacheCmd) fromCacheOrRun(ctx context.Context) (exitcode int, err error) {
	if err := c.makeCacheDir(); err != nil {
		return 0, err
	}

	base := c.cacheFilePath()
	stdoutCache := base + ".STDOUT"
	stderrCache := base + ".STDERR"
	exitCodeCache := base + ".EXIT_CODE"

	// Read from cache.
	if c.shouldUseCache(stdoutCache) {
		if err := c.fromCache(c.stdout, stdoutCache); err != nil {
			return 0, err
		}
		if err := c.fromCache(c.stderr, stderrCache); err != nil {
			return 0, err
		}
		code := c.readExitCodeFromCache(exitCodeCache)
		if !c.opt.async {
			return code, nil
		}
		// Spawn update command in background and return.
		return code, c.updateCacheCmd().Start()
	}

	stdoutf, finallyOut, cancelOut, err := c.prepareCacheFile(stdoutCache)
	if err != nil {
		return 0, err
	}
	defer func() { err = finallyOut() }()

	stderrf, finallyErr, cancelErr, err := c.prepareCacheFile(stderrCache)
	if err != nil {
		return 0, err
	}
	defer func() { err = finallyErr() }()

	// Run command.
	if err := c.runCmd(ctx, stdoutf, stderrf); err != nil {
		code, err := exitError(err)
		if err != nil {
			cancelOut()
			cancelErr()
			return code, err
		}
		if err := c.cacheExitCode(code, exitCodeCache); err != nil {
			return 0, err
		}
		return code, err
	}

	return 0, nil
}

// Create temp file to store command result.
// Do not use cache file directly to access cache file while updating cache.
func (c *CacheCmd) prepareCacheFile(path string) (
	f *os.File, finally func() error, cancel func(), err error) {
	tmpf, err := ioutil.TempFile(c.opt.cacheDir, "tmp_cachecmd_")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create temp file: %v", err)
	}
	cancelled := false
	finally = func() error {
		// Rename temp file to appropriate file name for cache.
		if err := tmpf.Close(); err != nil {
			return fmt.Errorf("failed to close file: %v", err)
		}
		// Clean up temp file in case rename failed.
		defer os.Remove(tmpf.Name())
		if cancelled {
			// Remove cache file if already exists.
			os.Remove(path)
			return nil
		}
		if err := os.Rename(tmpf.Name(), path); err != nil {
			return fmt.Errorf("faled to rename: %v", err)
		}
		return nil
	}
	cancelf := func() { cancelled = true }
	return tmpf, finally, cancelf, nil
}

func (c *CacheCmd) cacheExitCode(code int, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(fmt.Sprintf("%d", code))
	return err
}

func (c *CacheCmd) readExitCodeFromCache(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	b := make([]byte, 1)
	f.Read(b)
	code, _ := strconv.Atoi(string(b))
	return code
}

func (c *CacheCmd) updateCacheCmd() *exec.Cmd {
	execName := c.cachecmdExec
	if execName == "" {
		execName = os.Args[0]
	}
	args := append(c.cmdArgs[:0],
		append([]string{
			"-ttl", "0",
			"-cache_dir", c.opt.cacheDir,
			"-key", c.opt.cacheKey,
			c.cmdName},
			c.cmdArgs[0:]...)...)
	return exec.Command(execName, args...)
}

func (c *CacheCmd) shouldUseCache(cacheFname string) bool {
	if !fileexists(cacheFname) {
		return false
	}
	stat, err := os.Stat(cacheFname)
	if err != nil {
		return false
	}
	if c.currentTime.Second() == 0 {
		c.currentTime = time.Now()
	}
	return c.currentTime.Add(-c.opt.ttl).Sub(stat.ModTime()).Seconds() < 0
}

func (c *CacheCmd) fromCache(out io.Writer, cacheFname string) error {
	f, err := os.Open(cacheFname)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(out, f)
	return err
}

func (c *CacheCmd) makeCacheDir() error {
	return os.MkdirAll(c.opt.cacheDir, os.ModePerm)
}

func (c *CacheCmd) cacheFilePath() string {
	return filepath.Join(c.opt.cacheDir, c.cacheFileName())
}

func (c *CacheCmd) cacheFileName() string {
	h := md5.New()
	io.WriteString(h, c.opt.cacheKey)
	io.WriteString(h, ":")
	io.WriteString(h, c.cmdName+" "+strings.Join(c.cmdArgs, " "))
	return fmt.Sprintf("v%s-%x", cacheStructureVersion, h.Sum(nil))
}

func (c *CacheCmd) runCmd(ctx context.Context, stdoutCache, stderrCache io.Writer) error {
	cmd := exec.CommandContext(ctx, c.cmdName, c.cmdArgs...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if _, err := io.Copy(stdoutCache, io.TeeReader(stdout, c.stdout)); err != nil {
		return fmt.Errorf("failed to copy stdout to cache: %v", err)
	}
	if _, err := io.Copy(stderrCache, io.TeeReader(stderr, c.stderr)); err != nil {
		return fmt.Errorf("failed to copy stderr to cache: %v", err)
	}

	return cmd.Wait()
}

func fileexists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

func cacheDir() string {
	return filepath.Join(xdgCacheHome(), "cachecmd")
}

// REF: https://specifications.freedesktop.org/basedir-spec/basedir-spec-0.6.html
func xdgCacheHome() string {
	path := os.Getenv("XDG_CACHE_HOME")
	if path == "" {
		path = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return path
}

func exitError(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exiterr, ok := err.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus(), nil
		}
	}
	return 1, err
}
