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

var (
	camIP    *string
	cacheDir *string
	outDir   *string
	skipMov  *bool
	skipRaw  *bool
	copyDays *int
)

func init() {
	camIP = flag.String("cam-ip", "http://192.168.0.10", "camera ip")
	cacheDir = flag.String("cache-dir", ".cache", "cache directory")
	outDir = flag.String("out-dir", "output", "output directory")
	skipMov = flag.Bool("skip-movie", false, "skips mov files")
	skipRaw = flag.Bool("skip-raw", false, "skips raw files")
	copyDays = flag.Int("copy-days", 1, "specifies number of days to copy images from")

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

	// "http://192.168.0.10/get_imglist.cgi?DIR=%s"
	listImgs := *camIP + "/get_imglist.cgi?DIR=/DCIM/100OLYMP"
	getImg := *camIP + "%s"

	exif.RegisterParsers(mknote.All...)

	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
	signal.Notify(interruptions, os.Interrupt)
	go func() {
		<-interruptions
		cancel()
	}()

	dirSep := string(os.PathSeparator)
	fileUrls := make(chan string)

	client := http.Client{
		Transport: httpcache.NewTransport(diskcache.New(*cacheDir)),
	}

	go func() {
		defer close(fileUrls)
		r0, _ := http.NewRequestWithContext(appCtx, http.MethodGet, listImgs, nil)
		resp, err := client.Do(r0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not list imgs, used GET %s and failed with %v\n", listImgs, err)
			cancel()
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

	var (
		skip   bool
		wg     sync.WaitGroup
		doOnce sync.Once
	)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-appCtx.Done():
					return
				default:
					x, ok := <-fileUrls
					if !ok || skip {
						return
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
					ct := resp0.Header.Get("Content-Type")
					if *skipMov && strings.EqualFold(ct, "video/quicktime") {
						continue
					}
					if *skipRaw && !strings.EqualFold(ct, "image/jpeg") {
						continue
					}

					r2, err := http.NewRequestWithContext(appCtx, http.MethodGet, imgURL, nil)
					if err != nil {
						fmt.Fprintf(os.Stderr, "GET %s failed with %v\n", imgURL, err)
						continue
					}
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
						doOnce.Do(func() {
							skip = true
						})
						continue
					}

					p := *outDir + dirSep + fn
					f, err := os.Create(p)
					if err != nil {
						fmt.Fprintf(os.Stderr, "file create %s failed with %v\n", fn, err)
						continue
					}
					defer f.Close()
					_, err = io.Copy(f, bytes.NewReader(body))
					if err == nil {
						fmt.Printf("saving file to %s\n", p)
						err := f.Sync()
						if err != nil {
							fmt.Fprintf(os.Stderr, "file create %s failed with %v\n", fn, err)
							continue
						}
					} else {
						os.Remove(p)
						fmt.Fprintf(os.Stderr, "copy failed with %v\n", err)
					}
				}
			}
		}()
	}
	wg.Wait()
	close(interruptions)
}
