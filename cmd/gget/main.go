package main

import (
	"context"
	"flag"
	"fmt"
	"olyshare/gget"
	"os"
	"os/signal"
	"syscall"
)

var (
	outDir   string
	routines int
)

func init() {
	flag.StringVar(&outDir, "outdir", "./", "output directory")
	flag.IntVar(&routines, "routines", 2, "specifies number of routines used to download at a time")

	flag.Parse()
}

func main() {
	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-interruptions
		cancel()
	}()

	err := gget.Download(appCtx, os.Stdin, routines, outDir)
	if err != nil {
		fmt.Printf("error while downloding: %v\n", err)
	}
	close(interruptions)
}
