package camera

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/mknote"
)

func init() {
	exif.RegisterParsers(mknote.All...)
}

type Camera struct {
	IP        string
	ImagesURL string
}

type SkipFilter[T any] func(*T) bool

func (c *Camera) ListImages(ctx context.Context, skipFilters []SkipFilter[Image], ImporterFunc ImporterSource) ([]*Image, error) {
	var images []*Image
	defer func() {
		if ctx.Err() != nil {
			images = nil
		} else {
			// reverse the images as the latest ones are at the end
			for i, j := 0, len(images)-1; i < j; i, j = i+1, j-1 {
				images[i], images[j] = images[j], images[i]
			}
		}
	}()

	buf, err := ImporterFunc()
	if err != nil {
		return images, err
	}

	lines, err := ScanLines(bytes.NewReader(buf))
	if err != nil {
		return images, err
	}

	imageLinks := Filter(lines, []func(string) bool{
		func(a string) bool {
			return !strings.HasPrefix(a, "/")
		},
	})
	images = MakeImages(imageLinks, skipFilters)
	return images, nil
}

func MakeImages(a []string, skipFilters []SkipFilter[Image]) []*Image {
	var images []*Image
	for _, txt := range a {
		parts := strings.Split(txt, ",")
		if len(parts) < 2 {
			continue
		}
		// TODO: check len cond on parts
		fn := strings.Join(parts[:2], "/")
		img := &Image{
			ID: fn,
		}
		skipped := false
		for _, f := range skipFilters {
			if f(img) {
				skipped = true
				break
			}
		}
		if !skipped {
			images = append(images, img)
		}
	}
	return images
}

func Filter[T any](a []T, filters []func(a T) bool) []T {
	var results []T
	for _, l := range a {
		skipped := false
		for _, f := range filters {
			if f(l) {
				skipped = true
				break
			}
		}
		if !skipped {
			results = append(results, l)
		}
	}
	return results
}

func ScanLines(r io.Reader) ([]string, error) {
	var links []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		links = append(links, strings.TrimSpace(scanner.Text()))
	}
	return links, scanner.Err()
}

type Image struct {
	ID    string
	Taken time.Time
	Type  string
}

func (i *Image) String() string {
	return fmt.Sprintf("id: %s, taken: %s, type: %s", i.ID, i.Taken, i.Type)
}

func (i *Image) Id() string {
	fn := strings.Split(i.ID, "/")

	return fn[len(fn)-1]
}

func (i *Image) ContentType(ctx context.Context, camIP string, cli *http.Client) (contentType string, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, imgURL, nil)
	if err != nil {
		return
	}
	resp, err := cli.Do(req)
	if err != nil {
		return
	}
	contentType = resp.Header.Get("Content-Type")
	i.Type = contentType
	return
}

func (i *Image) Grab(ctx context.Context, camIP string, cli *http.Client) (body []byte, taken time.Time, err error) {
	imgURL := fmt.Sprintf(camIP+"%s", i.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return
	}
	fmt.Printf("dowloading image %s\n", imgURL)
	resp, err := cli.Do(req)
	if err != nil {
		return
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			fmt.Printf("resp body close %s failed with %v", imgURL, err)
		}
	}()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	xif, err := exif.Decode(bytes.NewReader(body))
	if err != nil {
		return
	}

	taken, err = xif.DateTime()
	fmt.Printf("dowloaded image %s taken on %s\n", imgURL, taken.Format(time.RubyDate))

	return body, taken, err
}

type Importer struct {
	ImporterSource   ImporterSource
	SkipContentTypes map[string]struct{}
	CopyDays         int
	WriteDir         string
	ImportRoutines   int
	SaveHandler      FileWriter
}

func (i *Importer) Import(ctx context.Context, cam *Camera, cli *http.Client) (err error) {
	images, err := cam.ListImages(ctx, []SkipFilter[Image]{
		func(img *Image) bool {
			p := filepath.Join(i.WriteDir, img.Id())
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
	}, i.ImporterSource)
	if err != nil || len(images) == 0 {
		return
	}

	errchan := make(chan error)
	imgchan := make(chan *Image)
	go func() {
		defer close(imgchan)
		for _, img := range images {
			imgchan <- img
		}
	}()
	var wg sync.WaitGroup
	go func() {
		defer close(errchan)
		wg.Wait()
	}()
	doneCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for j := 0; j < i.ImportRoutines; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case img, ok := <-imgchan:
					if !ok {
						cancel()
						return
					}
					err := i.StoreImage(doneCtx, cli, img, cam.IP)
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

func (i *Importer) StoreImage(ctx context.Context, cli *http.Client, img *Image, camIP string) error {
	body, taken, err := img.Grab(ctx, camIP, cli)
	if err != nil {
		return err
	}
	img.Taken = taken
	if i.CopyDays > 0 {
		dur := time.Since(taken)
		if int(math.Ceil(dur.Hours()/24)) > i.CopyDays {
			return fmt.Errorf("remaining images are older than %s so stopping here %d", taken, i.CopyDays)
		}
	}

	p := filepath.Join(i.WriteDir, img.Id())

	err = i.SaveHandler(body, p)
	if err != nil {
		return err
	}
	fmt.Printf("imported file to %s\n", p)
	return nil
}

type FileWriter func(data []byte, path string) error

type ImporterSource func() ([]byte, error)

func CameraLinksImporter(ctx context.Context, cli *http.Client, ip string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ip, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			fmt.Printf("resp body close %s failed with %v", ip, err)
		}
	}()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return buf, err
}

func FileLinksImporter(path string) ([]byte, error) {
	return os.ReadFile(path)
}

var TestURLsImporter = func(path string) ([]byte, error) {
	return FileLinksImporter(path)
}

var FileSaver = func(data []byte, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("file create %s failed with %v", path, err)
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("file close %s failed with %v", path, err)
		}
	}()
	_, err = io.Copy(f, bytes.NewReader(data))
	if err == nil {
		err := f.Sync()
		if err != nil {
			return fmt.Errorf("file create %s failed with %v", path, err)
		}
	}

	return nil
}
