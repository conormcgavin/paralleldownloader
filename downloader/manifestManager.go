package downloader

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"

	_ "github.com/lib/pq"
)

type ManifestManager interface {
	StartRequestListener()
}

type manifestManager struct {
	requestChan chan Request
	exitChan    chan bool
	directory   string
	lock        *sync.Mutex
}

func newManifestManager(directory string) *manifestManager {
	return &manifestManager{
		requestChan: make(chan Request),
		exitChan:    make(chan bool),
		directory:   directory,
		lock:        new(sync.Mutex),
	}
}

func NewManifestManager(directory string) *manifestManager {
	return newManifestManager(directory)
}

func (mm *manifestManager) StartRequestListener() {
	go mm.waitForRequest(mm.directory)
}

func (mm *manifestManager) waitForRequest(directory string) {
	fmt.Println("Manifest Manager: Listening for updates.")
	for {
		select {
		case req := <-mm.requestChan:
			fmt.Println("Manifest update received.")
			mm.UpdateManifest(req, directory)
		case <-mm.exitChan:
			return
		}
	}
}

func (mm *manifestManager) UpdateManifest(req Request, directory string) { // shoudl be lower case
	mm.lock.Lock()
	mfPath := fmt.Sprintf("%s/%d/manifest.json", directory, req.ID)
	jsonMf, _ := json.MarshalIndent(req, "", "	")
	bs := []byte(jsonMf)
	ioutil.WriteFile(mfPath, bs, 0755)
	mm.lock.Unlock()
}
