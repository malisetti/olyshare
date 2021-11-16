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
	"path/filepath"
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

type Camera struct {
	IP        string
	ImagesURL string
}

func (c *Camera) ListImages(ctx context.Context, cli *http.Client, skipFilters []func(*Image) bool) (<-chan *Image, error) {
	imgchan := make(chan *Image)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ImagesURL, nil)
	if err != nil {
		return imgchan, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return imgchan, err
	}
	go func() {
		defer close(imgchan)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			txt := scanner.Text()
			if strings.HasPrefix(txt, "/") {
				parts := strings.Split(txt, ",")
				fn := strings.Join(parts[:2], "/")
				img := &Image{
					ID: fn,
				}
				var skip bool
				for _, f := range skipFilters {
					if f(img) {
						skip = true
						break
					}
				}
				if !skip {
					defer func(fn string) {
						imgchan <- img
					}(fn)
				}
			}

			if err = scanner.Err(); err != nil {
				return
			}
		}
	}()

	return imgchan, nil
}

type Image struct {
	ID    string
	Taken time.Time
	Type  string
}

func (i *Image) ContentType(ctx context.Context, camIP string, cli *http.Client) (contentType string, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, imgURL, nil)
	if err != nil {
		return
	}
	resp0, err := cli.Do(req)
	if err != nil {
		return
	}
	contentType = resp0.Header.Get("Content-Type")
	i.Type = contentType
	return
}

func (i *Image) Grab(ctx context.Context, camIP string, cli *http.Client) (body *[]byte, taken time.Time, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	fmt.Printf("grabbing image %s\n", imgURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return
	}
	resp, err := cli.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var b []byte
	b, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var xif *exif.Exif
	xif, err = exif.Decode(bytes.NewReader(b))
	if err != nil {
		return
	}

	taken, err = xif.DateTime()
	return &b, taken, err
}

type Importer struct {
	SkipContentTypes map[string]struct{}
	CopyDays         int
	WriteDir         string
	ImportRoutines   int
}

func (i *Importer) Import(ctx context.Context, cam *Camera, cli *http.Client) (err error) {
	imgchan, err := cam.ListImages(ctx, cli, []func(img *Image) bool{
		func(img *Image) bool {
			fn := strings.Split(img.ID, "/")
			p := filepath.Join(i.WriteDir, fn[len(fn)-1])
			if _, err := os.Stat(p); err == nil {
				return true
			}
			return false
		},
		func(img *Image) bool {
			ct, err := img.ContentType(ctx, cam.IP, cli)
			if err != nil {
				return true
			}
			if _, ok := i.SkipContentTypes[ct]; ok {
				return true
			}
			return false
		},
	})
	if err != nil {
		return
	}

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
			for {
				select {
				case img := <-imgchan:
					if img == nil {
						return
					}
					err := i.StoreImage(ctx, cli, img, cam.IP)
					if err != nil {
						errchan <- err
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	return <-errchan
}

func (i *Importer) StoreImage(ctx context.Context, cli *http.Client, img *Image, camIP string) error {
	var body *[]byte
	var taken time.Time
	body, taken, err := img.Grab(ctx, camIP, cli)
	if err != nil {
		return err
	}
	img.Taken = taken
	cx := carbon.Now().SubDays(i.CopyDays)
	if carbon.NewCarbon(taken).Unix() < cx.Unix() {
		return fmt.Errorf("all images are older so stopping here")
	}

	fn := strings.Split(img.ID, "/")
	p := filepath.Join(i.WriteDir, fn[len(fn)-1])
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
	fmt.Printf("imported file to %s\n", p)
	return nil
}
