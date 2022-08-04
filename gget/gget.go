package gget

import (
	"bufio"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func Download(ctx context.Context, r io.Reader, routines int, outdir string) error {
	errchan := make(chan error)
	links := make(chan string)
	go func() {
		defer close(links)
		scanner := bufio.NewScanner(r)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if scanner.Scan() {
					links <- strings.TrimSpace(scanner.Text())
				} else {
					err := scanner.Err()
					if err != nil {
						errchan <- err
					}
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	go func() {
		defer close(errchan)
		wg.Wait()
	}()
	doneCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cli := http.DefaultClient
	for j := 0; j < routines; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case link, ok := <-links:
					if !ok {
						cancel()
						return
					}
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
					if err != nil {
						errchan <- err
					}
					resp, err := cli.Do(req)
					if err != nil {
						errchan <- err
					}
					defer func() {
						err := resp.Body.Close()
						if err != nil {
							errchan <- fmt.Errorf("resp body close %s failed with %v", link, err)
							cancel()
						}
					}()
					h := md5.New()
					io.WriteString(h, link)
					path := filepath.Join(outdir, fmt.Sprintf("%x", h.Sum(nil)))
					f, err := os.Create(path)
					if err != nil {
						errchan <- fmt.Errorf("file create %s failed with %v", path, err)
						cancel()
					}
					defer func() {
						err := f.Close()
						if err != nil {
							errchan <- fmt.Errorf("file close %s failed with %v", path, err)
							cancel()
						}
					}()
					_, err = io.Copy(f, resp.Body)
					if err == nil {
						err := f.Sync()
						if err != nil {
							errchan <- fmt.Errorf("file create %s failed with %v", path, err)
							cancel()
						}
					}
					if err != nil {
						errchan <- err
						cancel()
						return
					}
				case <-doneCtx.Done():
					return
				}
			}
		}()
	}

	return <-errchan
}
