package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

type Request struct {
	ID                  int    `json:"id"`
	Url                 string `json:"url"`
	Orig_data_file_name string `json:"orig_data_file_name"`
	Status              string `json:"status"`
	Submission_time     int64  `json:"submission_time"`
	Start_time          int64  `json:"start_time"`
	End_time            int64  `json:"end_time"`
}

type responseSaver struct {
	req  Request
	resp http.Response
}

type server struct {
	Router          *mux.Router
	nextID          int
	directory       string
	lock            *sync.Mutex // must change
	manifestManager manifestManager
	saver           chan responseSaver
	exit            chan bool
}

type Server interface {
	Get(w http.ResponseWriter, r *http.Request)
	Download(w http.ResponseWriter, r *http.Request)
	Delete(w http.ResponseWriter, r *http.Request)
	HandleRequests()
	Run()
}

func newServer(directory string) (*server, error) {
	s := server{
		Router:          mux.NewRouter(),
		directory:       directory,
		nextID:          0,
		lock:            new(sync.Mutex),
		manifestManager: NewManifestManager(directory),
		saver:           make(chan responseSaver),
	}
	s.manifestManager.StartRequestListener()
	go s.waitForResponse()

	if err := s.serverStartupCheck(); err != nil {
		return nil, fmt.Errorf("error creating server, startup check failed with following error: %s", err.Error())
	}

	return &s, nil
}

func NewServer(directory string) (*server, error) {
	return newServer(directory)
}

func (s *server) HandleRequests() {
	s.Router.HandleFunc("/resourceDownloader", s.Download).Methods("POST")
	s.Router.HandleFunc("/resourceDownloader/{id:[0-9]+}", s.Get).Methods("GET")
	s.Router.HandleFunc("/resourceDownloader/{id:[0-9]+}", s.Delete).Methods("DELETE")
}

func (s *server) Run() {
	log.Fatal(http.ListenAndServe(":8080", s.Router))
}

func (s *server) serverStartupCheck() error {
	fmt.Println("Start up commenced. Checking for incomplete requests from last run.")
	dirInfo, err := ioutil.ReadDir(s.directory)
	if err != nil {
		if err = os.Mkdir(s.directory, 0755); err != nil {
			return err
		}
	}
	var redoList []Request
	lastReq := "0"
	for _, dir := range dirInfo { // check last time created instead of greatest name?
		if dir.IsDir() {
			if dir.Name() > lastReq {
				lastReq = dir.Name()
			}

			req, err := s.GetManifest(dir.Name())
			if err != nil {
				return err
			}
			if req.Status == "in progress" || req.Status == "pending" {
				fmt.Printf("Must redo unfinished download request. ID: %s\n", dir.Name())
				redoList = append(redoList, *req)
			}
		}
	}

	for _, req := range redoList {
		go func(req Request) {
			resp, err := http.Get(req.Url)
			if err != nil {
				req.End_time = time.Now().UnixNano()
				req.Status = "failed"
				s.manifestManager.requestChan <- req
				return
			}
			rs := responseSaver{
				req:  req,
				resp: *resp,
			}
			s.saver <- rs
		}(req)
	}
	nextID, err := strconv.Atoi(lastReq)
	s.nextID = nextID + 1
	if err != nil {
		return err
	}
	return nil
}

func (s *server) Download(w http.ResponseWriter, r *http.Request) {
	directory := fmt.Sprintf("%s/%d", s.directory, s.nextID)
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		s.respondWithError(w, http.StatusInternalServerError, "Request ID already used.")
		return
	}
	if err := os.Mkdir(directory, 0755); err != nil {
		s.respondWithError(w, http.StatusInternalServerError, "Error creating request directory.")
		return
	}

	var req Request
	req.Submission_time = time.Now().UnixNano()
	req.ID = s.nextID
	s.nextID += 1
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		s.respondWithError(w, http.StatusInternalServerError, err.Error())
	}
	defer r.Body.Close()

	splitUrl := strings.Split(req.Url, "/")
	req.Orig_data_file_name = splitUrl[len(splitUrl)-1]
	req.Start_time = time.Now().UnixNano()
	req.Status = "pending"
	s.manifestManager.requestChan <- req
	s.respondWithJSON(w, http.StatusOK, map[string]string{"status": "Download request received."})

	go s.DownloadFile(w, r, req)
}

func (s *server) DownloadFile(w http.ResponseWriter, r *http.Request, req Request) { // adding 1 it returns immediately. Adding second updates the manifest, then waits for other to finish downloading before starting.
	resp, err := http.Get(req.Url)
	if err != nil {
		req.End_time = time.Now().UnixNano()
		req.Status = "failed"
		s.manifestManager.requestChan <- req
		return
	}
	req.Status = "in progress"
	s.manifestManager.requestChan <- req

	rs := responseSaver{
		req:  req,
		resp: *resp,
	}
	s.saver <- rs

}

func (s *server) Get(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	req, err := s.GetManifest(id)
	if err != nil {
		switch err.(type) {
		case *os.PathError:
			s.respondWithError(w, http.StatusNotFound, "Not found!")
		case *json.UnmarshalTypeError:
			s.respondWithError(w, http.StatusInternalServerError, "bad request?")
		}
	}
	s.respondWithJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

func (s *server) GetManifest(id string) (*Request, error) {
	filePath := fmt.Sprintf("%s/%s/manifest.json", s.directory, id)
	fmt.Printf("Retrieving information from file %s\n", filePath)
	jsonFile, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
	jsonReq, _ := ioutil.ReadAll(jsonFile)
	var req Request
	if err := json.Unmarshal(jsonReq, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (s *server) Delete(w http.ResponseWriter, r *http.Request) {
	s.lock.Lock()
	defer s.lock.Unlock()
	id := mux.Vars(r)["id"]
	directory := fmt.Sprintf("%s/%s", s.directory, id)

	if _, err := os.Stat(directory); os.IsNotExist(err) {
		s.respondWithError(w, http.StatusNotFound, "Not Found!")
		return
	}
	if err := os.RemoveAll(directory); err != nil {
		s.respondWithError(w, http.StatusInternalServerError, "error removing directory.")
		return
	}
	s.respondWithJSON(w, http.StatusOK, "Deleted.")
}

func (s *server) respondWithError(w http.ResponseWriter, code int, message string) {
	s.respondWithJSON(w, code, map[string]string{"error": message})
}

func (s *server) respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, _ := json.Marshal(payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func getFileExtension(fileName string) string { // doesn't work with some links!
	return strings.Split(fileName, ".")[1]
}

func (s *server) waitForResponse() {
	fmt.Println("Response Manager: Waiting to save data.")
	for {
		select {
		case rs := <-s.saver:
			fmt.Printf("Writing response to file. Request ID: %d. Original URL: %s", rs.req.ID, rs.req.Orig_data_file_name)
			s.saveResponse(rs)
		case <-s.exit:
			return
		}
	}
}

func (s *server) saveResponse(rs responseSaver) {
	defer rs.resp.Body.Close()
	s.lock.Lock()
	defer s.lock.Unlock()
	filePath := fmt.Sprintf("%s/%d/data.%s", s.directory, rs.req.ID, getFileExtension(rs.req.Orig_data_file_name))
	out, err := os.Create(filePath)
	if err != nil {
		rs.req.End_time = time.Now().UnixNano()
		rs.req.Status = "failed"
		s.manifestManager.requestChan <- rs.req
		return
	}
	defer out.Close()
	if _, err = io.Copy(out, rs.resp.Body); err != nil {
		rs.req.End_time = time.Now().UnixNano()
		rs.req.Status = "failed"
		s.manifestManager.requestChan <- rs.req
		return
	}

	rs.req.End_time = time.Now().UnixNano()
	rs.req.Status = "success"
	s.manifestManager.requestChan <- rs.req
}

/*
------------TO-DO-------------

1. Synchronisation (talk to Danis)
2. Testing
3. No errors on manifest manager atm
4.

------------------------------
*/
