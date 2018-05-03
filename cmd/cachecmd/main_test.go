package main

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheCmd_Run(t *testing.T) {
	stderr := ioutil.Discard

	cmd := "date"
	args := []string{`+%N`}

	tests := []struct {
		name       string
		opt1, opt2 option
		interval   time.Duration // interval between 2 command run
		wantCache  bool
	}{
		{
			name:      "from cache",
			opt1:      option{ttl: 2 * time.Second},
			opt2:      option{ttl: 2 * time.Second},
			interval:  time.Second,
			wantCache: true,
		},
		{
			name:      "cache expired",
			opt1:      option{ttl: 2 * time.Second},
			opt2:      option{ttl: 2 * time.Second},
			interval:  10 * time.Second,
			wantCache: false,
		},
		{
			name:      "force cache update",
			opt1:      option{ttl: 2 * time.Second},
			opt2:      option{ttl: 0},
			interval:  time.Second,
			wantCache: false,
		},
	}

	for _, tt := range tests {
		tmpdir, _ := ioutil.TempDir("", "cachecmdtest")
		defer os.RemoveAll(tmpdir)

		tt.opt1.cacheDir = tmpdir
		tt.opt2.cacheDir = tmpdir

		now := time.Now()

		stdout1 := new(bytes.Buffer)
		cachecmd := CacheCmd{
			stdout:  stdout1,
			stderr:  stderr,
			cmdName: cmd,
			cmdArgs: args,
			opt:     tt.opt1,

			currentTime: now,
		}

		if _, err := cachecmd.Run(context.TODO()); err != nil {
			t.Errorf("unexpected error w/ first run: %v", err)
			continue
		}

		stdout2 := new(bytes.Buffer)
		cachecmd.stdout = stdout2
		cachecmd.opt = tt.opt2
		cachecmd.currentTime = now.Add(tt.interval)

		if _, err := cachecmd.Run(context.TODO()); err != nil {
			t.Errorf("unexpected error w/ second run: %v", err)
			continue
		}

		gotCache := stdout1.String() == stdout2.String()
		if tt.wantCache != gotCache {
			t.Errorf("%s: got result from cache=%v, want cache=%v",
				tt.name, gotCache, tt.wantCache)
		}
	}
}

func TestCacheCmd_Run_failcmd(t *testing.T) {
	cachecmd := CacheCmd{
		stdout:  ioutil.Discard,
		stderr:  ioutil.Discard,
		cmdName: "cmd_not_found",
		cmdArgs: nil,
		opt:     option{},
	}
	if _, err := cachecmd.Run(context.TODO()); err == nil {
		t.Error("got no error, want error")
	}
}

func TestCacheCmd_Run_async(t *testing.T) {
	bin, cleanup, err := prepareBinary(t)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	cmd := "date"
	args := []string{`+%N`}

	tmpdir, _ := ioutil.TempDir("", "cachecmdtest")
	defer os.RemoveAll(tmpdir)

	tests := []struct {
		name     string
		cacheKey string
	}{
		{
			name: "normal",
		},
		{
			name:     "with cache key",
			cacheKey: "key",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()

			opt := option{
				ttl:      10 * time.Second,
				cacheDir: tmpdir,
				async:    true,
				cacheKey: tt.cacheKey,
			}

			stdout1 := new(bytes.Buffer)
			cachecmd := CacheCmd{
				stdout:  stdout1,
				stderr:  ioutil.Discard,
				cmdName: cmd,
				cmdArgs: args,
				opt:     opt,

				currentTime:  now,
				cachecmdExec: bin,
			}

			if _, err := cachecmd.Run(context.TODO()); err != nil {
				t.Fatalf("unexpected error w/ first run: %v", err)
			}

			stdout2 := new(bytes.Buffer)
			cachecmd.stdout = stdout2
			cachecmd.currentTime = now.Add(time.Second)

			if _, err := cachecmd.Run(context.TODO()); err != nil {
				t.Fatalf("unexpected error w/ second run: %v", err)
			}

			if stdout1.String() != stdout2.String() {
				t.Error("got different result, want cached result from second run")
			}

			cachecmd.currentTime = now.Add(time.Second)
			if err := tryToGetNewResult(cachecmd, 50, 10*time.Millisecond, stdout1.String()); err != nil {
				t.Fatalf("unexpected error w/ third run: %v", err)
			}
		})
	}
}

func tryToGetNewResult(cachecmd CacheCmd, n int, interval time.Duration, cache string) error {
	if n < 1 {
		return errors.New("got cached result")
	}
	stdout := new(bytes.Buffer)
	cachecmd.stdout = stdout
	if _, err := cachecmd.Run(context.TODO()); err != nil {
		return err
	}
	if stdout.String() != cache {
		return nil
	}
	time.Sleep(interval)
	n--
	return tryToGetNewResult(cachecmd, n, interval, cache)
}

func prepareBinary(t *testing.T) (bin string, cleanup func(), err error) {
	const pkg = "github.com/haya14busa/cachecmd/cmd/cachecmd"
	tmpDir, _ := ioutil.TempDir("", "cachecmd")
	cleanup = func() {
		os.RemoveAll(tmpDir)
	}
	bin = filepath.Join(tmpDir, "cachecmd")
	err = exec.Command("go", "build", "-o", bin, pkg).Run()
	return bin, cleanup, err
}
