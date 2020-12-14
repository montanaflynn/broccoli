package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/montanaflynn/broccoli/fs"
)

var (
	bundle, _ = defaultGenerator().generate()
	br        = fs.New(false, bundle)
)

func defaultGenerator() *Generator {
	return &Generator{
		inputFiles: []string{"testdata"},
		quality:    11,
	}
}

func TestBroccoli(t *testing.T) {
	var (
		realPaths    []string
		virtualPaths []string
		totalSize    float64

		files []*fs.File
	)

	filepath.Walk("testdata", func(path string, info os.FileInfo, _ error) error {
		f, err := fs.NewFile(path)
		if err != nil {
			t.Fatal(err)
		}

		files = append(files, f)
		realPaths = append(realPaths, f.Fpath)
		totalSize += float64(info.Size())
		return nil
	})

	start := time.Now()
	bundle, err := fs.Pack(files, 11)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	br := fs.New(false, bundle)
	br.Walk("./testdata", func(path string, _ os.FileInfo, _ error) error {
		virtualPaths = append(virtualPaths, path)
		return nil
	})

	assert.Equal(t, realPaths, virtualPaths, "paths asymmetric")

	fmt.Println("testdata: elapsed time", elapsed)
	fmt.Printf("testdata: compression factor %.2fx\n", totalSize/float64(len(bundle)))

	_, err = br.Open("bad")
	assert.Equal(t, os.ErrNotExist, err)
	_, err = br.Stat("bad")
	assert.Equal(t, os.ErrNotExist, err)

	assert.Panics(t, func() {
		_ = fs.New(false, nil)
	}, "New must panic with empty bundle")

	err = br.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		return errors.New("walk error")
	})
	assert.EqualError(t, err, "walk error")

	br = fs.New(true, bundle)
	_, err = br.Open("testdata/index.html")
	assert.NoError(t, err)
}

func TestGenerate(t *testing.T) {
	walk := func(g *Generator, walkFn filepath.WalkFunc) {
		bundle, err := g.generate()
		if err != nil {
			t.Fatal(err)
		}

		br := fs.New(false, bundle)
		br.Walk("testdata", walkFn)
	}

	var (
		realPaths    []string
		virtualPaths []string
	)

	filepath.Walk("testdata", func(path string, _ os.FileInfo, _ error) error {
		path = strings.ReplaceAll(path, `\`, "/")
		realPaths = append(realPaths, path)
		return nil
	})

	g := defaultGenerator()
	walk(g, func(path string, _ os.FileInfo, _ error) error {
		virtualPaths = append(virtualPaths, path)
		return nil
	})

	// to be sure that generator without side-effects gives exactly the same file structure
	assert.Equal(t, realPaths, virtualPaths, "paths asymmetric")

	g = defaultGenerator()
	g.includeGlob = "*.html"
	walk(g, func(path string, info os.FileInfo, _ error) error {
		if !info.IsDir() && filepath.Ext(path) != ".html" {
			t.Fatalf("generated bundle should not include excluded files")
		}
		return nil
	})

	g = defaultGenerator()
	g.excludeGlob = "*.html"
	walk(g, func(path string, info os.FileInfo, _ error) error {
		if !info.IsDir() && filepath.Ext(path) == ".html" {
			t.Fatalf("generated bundle should not include excluded files")
		}
		return nil
	})

	g = defaultGenerator()
	g.useGitignore = true
	walk(g, func(path string, info os.FileInfo, _ error) error {
		// following .gitignore rules
		if info.IsDir() {
			if info.Name() == "contents" {
				t.Fatal("generated bundle contains excluded contents directory")
			}
		} else if info.Name() != ".gitignore" && info.Name() != "googleJS.js" {
			t.Fatal("generated bundle should include only googleJS.js file")
		}
		return nil
	})
}

func TestFile(t *testing.T) {
	_, err := fs.NewFile("bad")
	assert.Error(t, err)

	f, err := br.Open("testdata/index.html")
	assert.NoError(t, err)

	info, err := os.Stat("testdata/index.html")
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0444), f.Mode()) // const for files
	assert.Equal(t, info.ModTime().Truncate(time.Second), f.ModTime())

	stat, err := f.Stat()
	assert.NoError(t, err)
	assert.NotNil(t, stat)

	assert.NoError(t, f.Close())
	_, err = f.Read(nil)
	assert.Equal(t, os.ErrClosed, err)
	_, err = f.Readdir(0)
	assert.Error(t, os.ErrInvalid, err)
	assert.Equal(t, os.ErrClosed, f.Close())

	assert.Equal(t, "index.html", f.Name())
	assert.Equal(t, info.Size(), f.Size())
	assert.False(t, f.IsDir())
	assert.Nil(t, f.Sys())

	assert.NoError(t, f.Open())
	n, err := f.Read(make([]byte, 1))
	assert.NoError(t, err)
	assert.Equal(t, 1, n)

	dir, err := br.Open("testdata/html")
	assert.NoError(t, err)

	info, err = os.Stat("testdata/html")
	assert.NoError(t, err)
	assert.Equal(t, os.ModeDir, dir.Mode())
	assert.Equal(t, info.ModTime().Truncate(time.Second), dir.ModTime())
}

func TestFileSeek(t *testing.T) {
	f, err := br.Open("testdata/index.html")
	assert.NoError(t, err)

	assert.NoError(t, f.Close())
	_, err = f.Seek(0, 0)
	assert.Equal(t, os.ErrClosed, err)
	assert.NoError(t, f.Open())

	_, err = f.Seek(0, -1)
	assert.EqualError(t, err, "Seek: bad whence")

	var (
		data   = f.Data
		size   = int64(len(data))
		offset int64
	)
	const chunkSize = 32

	t.Run("Seek(whence=io.SeekStart)", func(t *testing.T) {
		offset++

		_, err := f.Seek(size+1, io.SeekStart)
		assert.EqualError(t, err, "Seek: bad offset")

		n, err := f.Seek(1, io.SeekStart)
		assert.NoError(t, err)
		assert.Equal(t, offset, n)

		b := make([]byte, chunkSize)
		_, err = f.Read(b)
		assert.Equal(t, data[offset:offset+chunkSize], b)

		offset += chunkSize
	})

	t.Run("Seek(whence=io.SeekCurrent)", func(t *testing.T) {
		offset++

		_, err := f.Seek(size+1, io.SeekCurrent)
		assert.EqualError(t, err, "Seek: bad offset")

		n, err := f.Seek(1, io.SeekCurrent)
		assert.NoError(t, err)
		assert.Equal(t, offset, n)

		b := make([]byte, chunkSize)
		_, err = f.Read(b)
		assert.Equal(t, data[offset:offset+chunkSize], b)

		offset += chunkSize
	})

	t.Run("Seek(whence=io.SeekEnd)", func(t *testing.T) {
		offset = size - chunkSize

		_, err := f.Seek(size+1, io.SeekEnd)
		assert.EqualError(t, err, "Seek: bad offset")

		n, err := f.Seek(chunkSize, io.SeekEnd)
		assert.NoError(t, err)
		assert.Equal(t, offset, n)

		b := make([]byte, chunkSize)
		_, err = f.Read(b)
		assert.Equal(t, data[offset:], b)
	})
}

func TestFileReaddir(t *testing.T) {
	g := Generator{
		inputFiles: []string{"testdata"},
		quality:    11,
	}

	bundle, err := g.generate()
	if err != nil {
		t.Fatal(err)
	}
	br := fs.New(false, bundle)

	dir, err := br.Open("testdata/readdir")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Readdir(count=-1)", func(t *testing.T) {
		infos, err := dir.Readdir(-1)
		assert.NoError(t, err)
		assert.Len(t, infos, 3)
	})

	t.Run("Readdir(count=0)", func(t *testing.T) {
		infos, err := dir.Readdir(0)
		assert.NoError(t, err)
		assert.Len(t, infos, 3)
	})

	t.Run("Readdir(count>0)", func(t *testing.T) {
		infos, err := dir.Readdir(1)
		assert.NoError(t, err)
		assert.Len(t, infos, 1)
		assert.Equal(t, "1.txt", infos[0].Name())

		infos, err = dir.Readdir(1)
		assert.NoError(t, err)
		assert.Len(t, infos, 1)
		assert.Equal(t, "2.txt", infos[0].Name())

		infos, err = dir.Readdir(1)
		assert.NoError(t, err)
		assert.Len(t, infos, 1)
		assert.Equal(t, "3.txt", infos[0].Name())

		_, err = dir.Readdir(1)
		assert.Error(t, err)
	})

	dir, _ = br.Open("testdata/readdir")
	dir.Fpath = "bad"
	_, err = dir.Readdir(1)
	assert.Equal(t, io.EOF, err)
}

func TestHttpFileServer(t *testing.T) {
	srv := httptest.NewServer(br.Serve("testdata"))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/index.html")
	assert.NoError(t, err)
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	assert.NoError(t, err)

	orig, err := ioutil.ReadFile("testdata/index.html")
	assert.NoError(t, err)

	assert.Equal(t, data, orig)
	t.Log(string(data))
}
