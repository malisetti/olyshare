package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/mknote"
	"github.com/uniplaces/carbon"
)

const listImgs string = "http://192.168.0.10/get_imglist.cgi?DIR=/DCIM/100OLYMP"

// "http://192.168.0.10/get_imglist.cgi?DIR=%s"
const getImg string = "http://192.168.0.10%s"

// /DCIM/100OLYMP/P8301116.JPG

// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
	cacheDir := flag.String("cache-dir", ".cache", "cache directory")
	outDir := flag.String("out-dir", "output", "output directory")
	skipMov := flag.Bool("skip-movie", false, "skips mov files")
	skipRaw := flag.Bool("skip-raw", false, "skips raw files")
	copyDays := flag.Int("copy-days", 1, "specifies number of days to copy images from")

	flag.Parse()

	for _, v := range []string{*cacheDir, *outDir} {
		if _, err := os.Stat(v); os.IsNotExist(err) {
			// path/to/whatever does not exist
			fmt.Fprintf(os.Stderr, "given output dir %s does not exist or output dir is not present at ., failed with %v\n", *outDir, err)
			return
		}
	}

	exif.RegisterParsers(mknote.All...)

	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, os.Interrupt, os.Kill)
	go func() {
		<-interruptions
		cancel()
	}()

	dirSep := string(os.PathSeparator)
	fileUrls := make(chan string)

	var wgo sync.WaitGroup
	wgo.Add(2)

	go func() {
		defer wgo.Done()
		defer close(fileUrls)
		resp, err := http.Get(listImgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not list imgs, used GET %s and failed with %v\n", listImgs, err)
			return
		}

		defer resp.Body.Close()
		ix := io.TeeReader(resp.Body, os.Stdout)
		scanner := bufio.NewScanner(ix)
		for {
			select {
			case <-appCtx.Done():
				return
			default:
				if !scanner.Scan() {
					return
				}
				txt := scanner.Text()
				if strings.HasPrefix(txt, "/") {
					parts := strings.Split(txt, ",")
					fn := strings.Join(parts[:2], "/")
					defer func(fn string) {
						fileUrls <- fn
					}(fn)
				}

				if err := scanner.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "reading response failed with %v\n", err)
				}
			}
		}
	}()

	go func() {
		client := http.Client{
			Transport: httpcache.NewTransport(diskcache.New(*cacheDir)),
		}
		defer wgo.Done()
		skip := false
		for x := range fileUrls {
			if skip {
				continue
			}
			parts := strings.Split(x, "/")
			fn := parts[len(parts)-1]
			if _, err := os.Stat(*outDir + dirSep + fn); err == nil {
				continue
			}
			imgURL := fmt.Sprintf(getImg, x)
			fmt.Printf("fetching image from %s\n", imgURL)
			r1, err := http.NewRequestWithContext(appCtx, http.MethodHead, imgURL, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "HEAD %s failed with %v\n", imgURL, err)
				continue
			}
			resp0, err := client.Do(r1)
			if err != nil {
				fmt.Fprintf(os.Stderr, "HEAD %s failed with %v\n", imgURL, err)
				continue
			}
			for name, values := range resp0.Header {
				for _, value := range values {
					fmt.Println(name, value)
				}
			}
			ct := resp0.Header.Get("Content-Type")
			if *skipMov && strings.EqualFold(ct, "video/quicktime") {
				continue
			}
			if *skipRaw && !strings.EqualFold(ct, "image/jpeg") {
				continue
			}

			r2, err := http.NewRequestWithContext(appCtx, http.MethodGet, imgURL, nil)
			resp, err := client.Do(r2)
			if err != nil {
				fmt.Fprintf(os.Stderr, "GET %s failed with %v\n", imgURL, err)
				continue
			}
			defer resp.Body.Close()
			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read response body, failed with %v\n", err)
				continue
			}

			xif, err := exif.Decode(bytes.NewReader(body))
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read exif data, failed with %v\n", err)
				continue
			}

			tm, _ := xif.DateTime()
			fmt.Println("Taken: ", tm)
			cx := carbon.Now().SubDays(*copyDays)
			if carbon.NewCarbon(tm).Unix() < cx.Unix() {
				skip = true
				continue
			}

			p := *outDir + dirSep + fn
			f, err := os.Create(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "file create %s failed with %v\n", fn, err)
				continue
			}
			_, err = io.Copy(f, bytes.NewReader(body))
			if err == nil {
				fmt.Printf("saving file to %s\n", p)
				f.Sync()
				f.Close()
			} else {
				os.Remove(*outDir + dirSep + fn)
				fmt.Fprintf(os.Stderr, "copy failed with %v\n", err)
			}
		}
	}()

	wgo.Wait()
	close(interruptions)
}
