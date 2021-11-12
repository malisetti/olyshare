package camera

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/mknote"
	"github.com/uniplaces/carbon"
)

func init() {
	exif.RegisterParsers(mknote.All...)
}

const DirSep = string(os.PathSeparator)

type Camera struct {
	IP        string
	ImagesURL string
}

func (c *Camera) ListImages(ctx context.Context, cli *http.Client) (images []*Image, err error) {
	r0, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ImagesURL, nil)
	if err != nil {
		return
	}
	resp, err := cli.Do(r0)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		txt := scanner.Text()
		if strings.HasPrefix(txt, "/") {
			parts := strings.Split(txt, ",")
			fn := strings.Join(parts[:2], "/")
			defer func(fn string) {
				images = append(images, &Image{
					ID: fn,
				})
			}(fn)
		}

		if err = scanner.Err(); err != nil {
			return
		}
	}
	return
}

type Image struct {
	ID string
}

func (i *Image) ContentType(ctx context.Context, camIP string, cli *http.Client) (contentType string, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	r1, err := http.NewRequestWithContext(ctx, http.MethodHead, imgURL, nil)
	if err != nil {
		return
	}
	resp0, err := cli.Do(r1)
	if err != nil {
		return
	}
	contentType = resp0.Header.Get("Content-Type")
	return
}

func (i *Image) Grab(ctx context.Context, camIP string, cli *http.Client) (body *[]byte, taken time.Time, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	r2, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return
	}
	resp, err := cli.Do(r2)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return &b, taken, err
	}

	xif, err := exif.Decode(bytes.NewReader(b))
	if err != nil {
		return
	}

	taken, err = xif.DateTime()
	return
}

type Importer struct {
	SkipContentTypes *[]string
	CopyDays         int
	WriteDir         string
	ImportRoutines   int
}

func (i *Importer) Import(ctx context.Context, cam *Camera, cli *http.Client) (err error) {
	images, err := cam.ListImages(ctx, cli)
	if err != nil {
		return
	}

	skipCtMap := make(map[string]string)
	for _, v := range *i.SkipContentTypes {
		skipCtMap[v] = v
	}

	ctxx, cancel := context.WithCancel(ctx)
	defer cancel()

	imchan := make(chan *Image)
	errchan := make(chan error)
	var wg sync.WaitGroup
	go func() {
		wg.Wait()
		close(errchan)
	}()

	for j := 0; j < i.ImportRoutines; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctxx.Err() == nil {
				select {
				case img := <-imchan:
					err := func(img *Image) error {
						var body *[]byte
						var taken time.Time
						body, taken, err = img.Grab(ctx, cam.IP, cli)
						if err != nil {
							return err
						}
						if i.CopyDays != 0 {
							cx := carbon.Now().SubDays(i.CopyDays)
							if carbon.NewCarbon(taken).Unix() < cx.Unix() {
								return nil
							}
						}

						p := i.WriteDir + DirSep + img.ID
						f, err := os.Create(p)
						if err != nil {
							return fmt.Errorf("file create %s failed with %v", img.ID, err)
						}
						defer f.Close()
						_, err = io.Copy(f, bytes.NewReader(*body))
						if err == nil {
							err := f.Sync()
							if err != nil {
								return fmt.Errorf("file create %s failed with %v", img.ID, err)
							}
						}
						return nil
					}(img)

					if err != nil {
						errchan <- err
						cancel()
						return
					}
				case <-ctxx.Done():
					return
				}
			}
		}()
	}

	for _, img := range images {
		if _, err := os.Stat(i.WriteDir + DirSep + img.ID); err == nil {
			continue
		}

		var ct string
		ct, err = img.ContentType(ctx, cam.IP, cli)
		if err != nil {
			return
		}
		if _, ok := skipCtMap[ct]; ok {
			continue
		}

		imchan <- img
	}

	close(imchan)

	return <-errchan
}
