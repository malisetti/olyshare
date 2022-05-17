package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
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

//go:embed src.zip
var source []byte

// /DCIM/100OLYMP/P8301116.JPG
// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
	if len(os.Args) >= 1 && os.Args[1] == "src" {
		s, err := os.Create("src.zip")
		if err != nil {
			panic(err)
		}
		_, err = io.Copy(s, bytes.NewReader(source))
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

	skipCtSet := make(map[string]struct{})
	if skipMov {
		skipCtSet["video/quicktime"] = struct{}{}
		skipCtSet["video/x-msvideo"] = struct{}{}
	}
	if skipRaw {
		skipCtSet["image/x-olympus-orf"] = struct{}{}
	}

	if importRoutines <= 0 || importRoutines >= 5 {
		importRoutines = 2
	}

	cam := &camera.Camera{
		IP:        camIP,
		ImagesURL: camIP + "/get_imglist.cgi?DIR=/DCIM/100OLYMP", // "http://192.168.0.10/get_imglist.cgi?DIR=%s"
	}

	imp := &camera.Importer{
		SkipContentTypes: skipCtSet,
		CopyDays:         copyDays,
		WriteDir:         outDir,
		ImportRoutines:   importRoutines,
		SaveHandler:      camera.FileSaver,
	}
	err := imp.Import(appCtx, cam, httpcache.NewTransport(diskcache.New(cacheDir)).Client())
	if err != nil {
		fmt.Printf("import error: %v\n", err)
	}
	close(interruptions)
}
