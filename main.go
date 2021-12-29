package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/mseshachalam/olyshare/camera"
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
	flag.String(&cacheDir, "cache-dir", ".cache", "cache directory")
	flag.String(&outDir, "out-dir", "output", "output directory")
	flag.Bool(&skipMov, "skip-movie", false, "skips mov files")
	flag.Bool(&skipRaw, "skip-raw", false, "skips raw files")
	flag.Int(&copyDays, "copy-days", 1, "specifies number of days to copy images from")
	flag.Int(&importRoutines, "import-routines", 2, "specifies number of routines used to copy images at a time")

	flag.Parse()
}

// /DCIM/100OLYMP/P8301116.JPG
// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
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
