package testutils

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/windmilleng/wmclient/pkg/os/temp"
)

type TempDirFixture struct {
	t   testing.TB
	ctx context.Context
	dir *temp.TempDir
}

func NewTempDirFixture(t testing.TB) *TempDirFixture {
	dir, err := temp.NewDir(t.Name())
	if err != nil {
		t.Fatalf("Error making temp dir: %v", err)
	}

	return &TempDirFixture{
		t:   t,
		ctx: CtxForTest(),
		dir: dir,
	}
}

func (f *TempDirFixture) T() testing.TB {
	return f.t
}

func (f *TempDirFixture) Ctx() context.Context {
	return f.ctx
}

func (f *TempDirFixture) Path() string {
	return f.dir.Path()
}

func (f *TempDirFixture) JoinPath(path ...string) string {
	p := []string{f.Path()}
	p = append(p, path...)
	return filepath.Join(p...)
}

func (f *TempDirFixture) JoinPaths(paths []string) []string {
	joined := make([]string, len(paths))
	for i, p := range paths {
		joined[i] = f.JoinPath(p)
	}
	return joined
}

func (f *TempDirFixture) WriteFile(path string, contents string) {
	fullPath := filepath.Join(f.Path(), path)
	base := filepath.Dir(fullPath)
	err := os.MkdirAll(base, os.FileMode(0777))
	if err != nil {
		f.t.Fatal(err)
	}
	err = ioutil.WriteFile(fullPath, []byte(contents), os.FileMode(0777))
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *TempDirFixture) TouchFiles(paths []string) {
	for _, p := range paths {
		f.WriteFile(p, "")
	}
}

func (f *TempDirFixture) Rm(pathInRepo string) {
	fullPath := filepath.Join(f.Path(), pathInRepo)
	err := os.Remove(fullPath)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (tempDir *TempDirFixture) NewFile(prefix string) (f *os.File, err error) {
	return ioutil.TempFile(tempDir.dir.Path(), prefix)
}

func (f *TempDirFixture) TearDown() {
	err := f.dir.TearDown()
	if err != nil {
		f.t.Fatal(err)
	}
}