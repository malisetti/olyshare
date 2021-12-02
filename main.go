package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mseshachalam/olyshare/camera"
)

var (
	camIP          *string
	outDir         *string
	skipMov        *bool
	skipRaw        *bool
	copyDays       *int
	importRoutines *int
)

func init() {
	camIP = flag.String("cam-ip", "http://192.168.0.10", "camera ip")
	outDir = flag.String("out-dir", "output", "output directory")
	skipMov = flag.Bool("skip-movie", false, "skips mov files")
	skipRaw = flag.Bool("skip-raw", false, "skips raw files")
	copyDays = flag.Int("copy-days", 1, "specifies number of days to copy images from")
	importRoutines = flag.Int("import-routines", 2, "specifies number of routines used to copy images at a time")

	flag.Parse()
}

// /DCIM/100OLYMP/P8301116.JPG
// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
	if stat, err := os.Stat(*outDir); os.IsNotExist(err) || !stat.IsDir() {
		fmt.Fprintf(os.Stderr, "output dir %s does not exist or is not a dir, failed with %v\n", *outDir, err)
		return
	}

	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-interruptions
		cancel()
	}()

	cam := &camera.Camera{
		IP:        *camIP,
		ImagesURL: *camIP + "/get_imglist.cgi?DIR=/DCIM/100OLYMP", // "http://192.168.0.10/get_imglist.cgi?DIR=%s"
	}

	skipCtMap := make(map[string]struct{})
	if *skipMov {
		skipCtMap["video/quicktime"] = struct{}{}
		skipCtMap["video/x-msvideo"] = struct{}{}
	}
	if *skipRaw {
		skipCtMap["image/x-olympus-orf"] = struct{}{}
	}

	if *importRoutines <= 0 || *importRoutines >= 5 {
		*importRoutines = 2
	}
	imp := &camera.Importer{
		SkipContentTypes: skipCtMap,
		CopyDays:         *copyDays,
		WriteDir:         *outDir,
		ImportRoutines:   *importRoutines,
	}
	err := imp.Import(appCtx, cam, &http.Client{})
	if err != nil {
		fmt.Printf("import error: %v\n", err)
	}
	close(interruptions)
}
