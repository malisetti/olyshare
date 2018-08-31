package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
)

const listImgs string = "http://192.168.0.10/get_imglist.cgi?DIR=/DCIM/100OLYMP"

// "http://192.168.0.10/get_imglist.cgi?DIR=%s"
const getImg string = "http://192.168.0.10%s"

// /DCIM/100OLYMP/P8301116.JPG

// /DCIM/100OLYMP,P3300029.JPG,2964502,0,19582,35122
func main() {
	appCtx, cancel := context.WithCancel(context.Background())
	interruptions := make(chan os.Signal, 1)
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
		scanner := bufio.NewScanner(resp.Body)
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
					fileUrls <- strings.Join(parts[:2], "/")
				}

				if err := scanner.Err(); err != nil {
					fmt.Fprintf(os.Stderr, "reading response failed with %v\n", err)
				}
			}
		}
	}()

	go func() {
		client := http.Client{
			Transport: httpcache.NewTransport(diskcache.New("cache")),
		}
		defer wgo.Done()
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for x := range fileUrls {
					parts := strings.Split(x, "/")
					fn := parts[len(parts)-1]
					if _, err := os.Stat("output" + dirSep + fn); err == nil {
						continue
					}
					imgURL := fmt.Sprintf(getImg, x)
					fmt.Printf("fetching image from %s\n", imgURL)
					resp, err := client.Get(imgURL)
					if err != nil {
						fmt.Fprintf(os.Stderr, "GET %s failed with %v\n", imgURL, err)
						continue
					}
					defer resp.Body.Close()
					f, err := os.Create("output" + dirSep + fn)
					if err != nil {
						fmt.Fprintf(os.Stderr, "file create %s failed with %v\n", fn, err)
						continue
					}
					_, err = io.Copy(f, resp.Body)
					if err == nil {
						fmt.Printf("saving file %s in output\n", fn)
						f.Sync()
						f.Close()
					} else {
						os.Remove("output" + dirSep + fn)
						fmt.Fprintf(os.Stderr, "copy failed with %v", err)
					}
				}
			}()
		}

		wg.Wait()
	}()

	wgo.Wait()
}
