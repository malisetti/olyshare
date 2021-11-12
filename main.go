package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/mseshachalam/olyshare/camera"
)

var (
	camIP          *string
	cacheDir       *string
	outDir         *string
	skipMov        *bool
	skipRaw        *bool
	copyDays       *int
	importRoutines *int
)

func init() {
	camIP = flag.String("cam-ip", "http://192.168.0.10", "camera ip")
	cacheDir = flag.String("cache-dir", ".cache", "cache directory")
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
	for _, v := range []string{*cacheDir, *outDir} {
		if _, err := os.Stat(v); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "given dir %s does not exist, failed with %v\n", *outDir, err)
			return
		}
	}

	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, os.Interrupt)
	go func() {
		<-interruptions
		cancel()
	}()

	client := http.Client{
		Transport: httpcache.NewTransport(diskcache.New(*cacheDir)),
	}

	cam := &camera.Camera{
		IP:        *camIP,
		ImagesURL: *camIP + "/get_imglist.cgi?DIR=/DCIM/100OLYMP", // "http://192.168.0.10/get_imglist.cgi?DIR=%s"
	}

	var skipContentTypes []string
	if *skipMov {
		skipContentTypes = append(skipContentTypes, "video/quicktime", "video/x-msvideo")
	}
	if *skipRaw {
		skipContentTypes = append(skipContentTypes, "image/x-olympus-orf")
	}
	if *importRoutines <= 0 || *importRoutines >= 5 {
		*importRoutines = 2
	}
	imp := &camera.Importer{
		SkipContentTypes: &skipContentTypes,
		CopyDays:         *copyDays,
		WriteDir:         *outDir,
		ImportRoutines:   *importRoutines,
	}
	err := imp.Import(appCtx, cam, &client)
	if err != nil {
		fmt.Printf("import error: %v\n", err)
	}
	close(interruptions)
}
