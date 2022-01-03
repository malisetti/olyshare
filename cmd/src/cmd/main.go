package main

import (
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"olyshare/camera"

	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
)

var (
	camIP          string
	cacheDir       string
	outDir         string
	skipMov        bool
	skipRaw        bool
	copyDays       int
	importRoutines int
)

func init() {
	flag.StringVar(&camIP, "cam-ip", "http://192.168.0.10", "camera ip")
	flag.StringVar(&cacheDir, "cache-dir", ".cache", "cache directory")
	flag.StringVar(&outDir, "out-dir", "output", "output directory")
	flag.BoolVar(&skipMov, "skip-movie", false, "skips mov files")
	flag.BoolVar(&skipRaw, "skip-raw", false, "skips raw files")
	flag.IntVar(&copyDays, "copy-days", 1, "specifies number of days to copy images from")
	flag.IntVar(&importRoutines, "import-routines", 2, "specifies number of routines used to copy images at a time")

	flag.Parse()
}

//go:generate rm -rf src
//go:generate mkdir -p src/cmd
//go:generate cp -r ../camera ../go.mod ../go.sum src/
//go:generate cp main.go src/cmd/
//go:generate mv src/go.mod src/x.mod
//go:generate mv src/go.sum src/x.sum
//go:embed src
var source embed.FS

// /DCIM/100OLYMP/P8301116.JPG
// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
	if len(os.Args) >= 1 && os.Args[1] == "src" {
		err := fs.WalkDir(source, "src", func(path string, d fs.DirEntry, err error) error {
			if d.IsDir() {
				err := os.MkdirAll(path, 0700)
				if err != nil {
					return err
				}
			} else {
				pathx := path
				if strings.HasPrefix(d.Name(), "x.") {
					pathx = filepath.Join(filepath.Dir(path), strings.Replace(d.Name(), "x.", "go.", 1))
				}
				f, err := os.Create(pathx)
				if err != nil {
					return err
				}
				src, err := source.ReadFile(path)
				if err != nil {
					return err
				}
				_, err = io.Copy(f, bytes.NewReader(src))
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
		return
	}
	for _, v := range []string{cacheDir, outDir} {
		if stat, err := os.Stat(v); os.IsNotExist(err) || !stat.IsDir() {
			fmt.Fprintf(os.Stderr, "given dir %s does not exist, failed with %v\n", outDir, err)
			return
		}
	}

	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-interruptions
		cancel()
	}()

	cam := &camera.Camera{
		IP:        camIP,
		ImagesURL: camIP + "/get_imglist.cgi?DIR=/DCIM/100OLYMP", // "http://192.168.0.10/get_imglist.cgi?DIR=%s"
	}

	skipCtMap := make(map[string]struct{})
	if skipMov {
		skipCtMap["video/quicktime"] = struct{}{}
		skipCtMap["video/x-msvideo"] = struct{}{}
	}
	if skipRaw {
		skipCtMap["image/x-olympus-orf"] = struct{}{}
	}

	if importRoutines <= 0 || importRoutines >= 5 {
		importRoutines = 2
	}
	imp := &camera.Importer{
		SkipContentTypes: skipCtMap,
		CopyDays:         copyDays,
		WriteDir:         outDir,
		ImportRoutines:   importRoutines,
	}
	err := imp.Import(appCtx, cam, httpcache.NewTransport(diskcache.New(cacheDir)).Client())
	if err != nil {
		fmt.Printf("import error: %v\n", err)
	}
	close(interruptions)
}
