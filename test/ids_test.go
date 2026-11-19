package test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// Access will hang on item ids, so an item keeps its id however the folder is listed around it. Here listings run
// without pause while files are uploaded: a listing that read the storage before an upload ended must not drop the new
// item and let the next listing add it back under another id. (internal/drive has the same race step by step.)
func TestIdsLastThroughListingsThatRaceUploads(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	folder := madeFolder(t, d.As(reka), placeID, 0, "Busy")

	var stop atomic.Bool
	var wg sync.WaitGroup
	seen := sync.Map{} // name → the first id a listing showed for it
	changed := make(chan string, 100)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := d.As(reka)
			for !stop.Load() {
				for _, folderID := range []int64{0, folder.ID} {
					res := c.Get(t, listPath(placeID, folderID))
					if res.Status != http.StatusOK {
						continue
					}
					var l itemsJSON
					res.JSON(t, &l)
					for _, it := range l.Items {
						if first, loaded := seen.LoadOrStore(it.Name, it.ID); loaded && first.(int64) != it.ID {
							select {
							case changed <- fmt.Sprintf("%s: id %d, then %d", it.Name, first, it.ID):
							default:
							}
						}
					}
				}
			}
		}()
	}

	uploaded := map[string]int64{}
	for i := range 40 {
		name := fmt.Sprintf("File %02d.txt", i)
		folderID := int64(0)
		if i%2 == 1 {
			folderID = folder.ID
		}
		uploaded[name] = uploadedInto(t, d.As(reka), placeID, folderID, name, []byte(name)).ID
	}
	stop.Store(true)
	wg.Wait()
	close(changed)
	reported := map[string]bool{}
	for msg := range changed {
		if !reported[msg] {
			reported[msg] = true
			t.Errorf("an id changed under a listing: %s", msg)
		}
	}
	for name, id := range uploaded {
		if first, ok := seen.Load(name); ok && first.(int64) != id {
			t.Errorf("%s was uploaded as %d, and a listing showed it as %d", name, id, first)
		}
	}
	all := append(list(t, d.As(reka), placeID, 0).Items, list(t, d.As(reka), placeID, folder.ID).Items...)
	for _, it := range all {
		if id, ok := uploaded[it.Name]; ok && id != it.ID {
			t.Errorf("%s was uploaded as %d and is now %d", it.Name, id, it.ID)
		}
	}
}

// While a replacing upload is on its way, it arrives in the hidden uploads folder, and the file it replaces stays where
// it is, whole, until the upload is complete: then the two swap. A listing meanwhile shows the file with its id, and it
// keeps its id through the replace.
func TestAReplacedFileKeepsItsIdWhileItsUploadIsOnItsWay(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	report := uploadedInto(t, d.As(reka), placeID, 0, "Report.pdf", []byte("first"))

	body, write := io.Pipe()
	done := make(chan harness.Response, 1)
	go func() {
		q := url.Values{"name": {"Report.pdf"}, "existing": {"replace"}}
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/places/%d/files?%s", d.URL, placeID, q.Encode()), body)
		req.ContentLength = 6
		req.Header.Set("Drive-Secret", d.Secret)
		req.Header.Set("Remote-User-Id", reka.UserID)
		req.Header.Set("Origin", d.Address)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- harness.Response{}
			return
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		done <- harness.Response{Status: res.StatusCode, Body: data}
	}()
	if _, err := write.Write([]byte("sec")); err != nil {
		t.Fatal(err)
	}
	harness.Eventually(t, "the upload is on its way into the uploads folder", func() bool {
		_, ok := d.Storage.List(t, ".superit-drive/uploads")
		return ok
	})
	time.Sleep(200 * time.Millisecond) // its first bytes are on their way to the storage
	if got, _ := d.Storage.Get(t, rekasTop+"/Report.pdf"); string(got) != "first" {
		t.Fatalf("during the replace Report.pdf holds %q, want the file it replaces, whole", got)
	}
	if l := list(t, d.As(reka), placeID, 0); len(l.Items) != 1 || l.Items[0].ID != report.ID {
		t.Fatalf("during the replace the list holds %+v, want Report.pdf with id %d", l.Items, report.ID)
	}
	_, _ = write.Write([]byte("ond"))
	_ = write.Close()
	res := <-done
	var it itemJSON
	res.JSON(t, &it)
	if res.Status != http.StatusCreated || it.ID != report.ID {
		t.Fatalf("the replace: got %d %s, want Report.pdf with id %d", res.Status, res.Body, report.ID)
	}
	if l := list(t, d.As(reka), placeID, 0); len(l.Items) != 1 || l.Items[0].ID != report.ID || *l.Items[0].Size != 6 {
		t.Fatalf("after the replace the list holds %+v", l.Items)
	}
}
