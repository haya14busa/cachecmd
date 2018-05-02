package main

import (
	"context"
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const version = "0.9.0"

const usageMessage = "" +
	`Usage: cachecmd [flags] command
`

func usage() {
	fmt.Fprintln(os.Stderr, usageMessage)
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	os.Exit(2)
}

type option struct {
	version bool
	ttl     time.Duration
}

var flagOpt = &option{}

func init() {
	flag.BoolVar(&flagOpt.version, "version", false, "print version")
	flag.DurationVar(&flagOpt.ttl, "ttl", time.Minute, "TTL(Time to live) of cache")
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flagOpt.version {
		fmt.Fprintln(os.Stderr, version)
		return
	}
	if err := run(os.Stdin, os.Stdout, os.Stderr, flagOpt, flag.Args()); err != nil {
		os.Exit(exitCode(err))
	}
}

func run(r io.Reader, w io.Writer, stderr io.Writer, opt *option, command []string) error {
	if len(command) == 0 {
		usage()
		return nil
	}
	cachecmd := CacheCmd{
		stdout:  w,
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
	opt     *option

	currentTime time.Time
}

func (c *CacheCmd) Run(ctx context.Context) error {
	if err := c.makeCacheDir(); err != nil {
		return err
	}

	cacheFname := c.cacheFileName()

	if c.shouldUseCache(cacheFname) {
		return c.fromCache(cacheFname)
	}

	f, err := os.Create(cacheFname)
	if err != nil {
		return err
	}
	defer f.Close()

	return c.runCmd(ctx, f)
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

func (c *CacheCmd) fromCache(cacheFname string) error {
	f, err := os.Open(cacheFname)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(c.stdout, f); err != nil {
		return err
	}
	return nil
}

func (c *CacheCmd) makeCacheDir() error {
	return os.MkdirAll(cacheDir(), os.ModePerm)
}

func (c *CacheCmd) cacheFileName() string {
	fname := cacheFileName(c.cmdName + " " + strings.Join(c.cmdArgs, " "))
	return filepath.Join(cacheDir(), fname)
}

func (c *CacheCmd) runCmd(ctx context.Context, cache io.Writer) error {
	cmd := exec.CommandContext(ctx, c.cmdName, c.cmdArgs...)
	cmd.Stderr = c.stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	go cmd.Start()
	if _, err := io.Copy(cache, io.TeeReader(stdout, c.stdout)); err != nil {
		return err
	}

	return cmd.Wait()
}

func fileexists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

func cacheFileName(cmd string) string {
	h := md5.New()
	io.WriteString(h, cmd)
	return fmt.Sprintf("%x", h.Sum(nil))
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

func exitCode(err error) int {
	if exiterr, ok := err.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}
