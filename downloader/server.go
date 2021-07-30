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
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

var DIRECTORY_ROOT = "downloader/requests"

type Manifest struct {
	Url                 string `json:"url"`
	Orig_data_file_name string `json:"orig_data_file_name"`
	Status              string `json:"status"`
	Submission_time     int64  `json:"submission_time"`
	Start_time          int64  `json:"start_time"`
	End_time            int64  `json:"end_time"`
}

type App struct{}

type Request struct {
	id      string
	dirName string
	body    *RequestBody
}

type RequestBody struct {
	Url                 string `json:"url"`
	Orig_data_file_name string
}

type Server interface {
	Get(w http.ResponseWriter, r *http.Request)
	Download(w http.ResponseWriter, r *http.Request)
	Delete(w http.ResponseWriter, r *http.Request)
	HandleRequests()
	Run()
}

type server struct {
	requestChannel chan http.Request
	terminate      chan bool
	Router         *mux.Router
	nextID         int
	app            App
}

func newServer() (*server, error) {
	s := server{
		requestChannel: make(chan http.Request),
		terminate:      make(chan bool),
		Router:         mux.NewRouter(),
		app:            App{},
	}
	nextID, err := s.serverStartupCheck()
	if err != nil {
		return nil, err
	}
	s.nextID = nextID
	return &s, nil
}

func NewServer() (*server, error) {
	return newServer()
}

func (s *server) serverStartupCheck() (int, error) {
	fmt.Println("Checking for uncomplete requests.")
	dirInfo, err := ioutil.ReadDir(DIRECTORY_ROOT)
	if err != nil {
		if err = os.Mkdir(DIRECTORY_ROOT, 0755); err != nil {
			return -1, fmt.Errorf("%s", err.Error())
		}
	}
	lastReq := "0"
	for _, dir := range dirInfo {
		if dir.IsDir() {
			directory := fmt.Sprintf("%s/%s", DIRECTORY_ROOT, dir.Name())
			m, err := s.app.GetManifest(directory)
			if err != nil {
				return -1, err
			}

			if dir.Name() > lastReq {
				lastReq = dir.Name()
			}

			if m.Status == "in progress" || m.Status == "pending" {
				fmt.Printf("Must redo unfinished download request. ID: %s", dir.Name()) // maybe make an actual http request rather than a fake one
				/*
					rb := RequestBody{
						Url:                 m.Url,
						Orig_data_file_name: m.Orig_data_file_name,
					}
					dirName := fmt.Sprintf("%s/%s", DIRECTORY_ROOT, dir.Name())
					req := Request{
						id:      dir.Name(),
						dirName: dirName,
						body:    &rb,
					}
					if err := s.Download(req); err != nil {
						s.respondWithError(w, 404, err.Error())
					}
					s.respondWithJSON(w, 200, "Download started.")
				*/
			}
		}
	}
	nextID, _ := strconv.Atoi(lastReq)
	return nextID, nil
}

func (s *server) HandleRequests() {
	s.Router.HandleFunc("/resourceDownloader", s.Download).Methods("POST")
	s.Router.HandleFunc("/resourceDownloader/{id:[0-9]+}", s.Get).Methods("GET")
	s.Router.HandleFunc("/resourceDownloader/{id:[0-9]+}", s.Delete).Methods("DELETE")
}

func (s *server) Run() {
	log.Fatal(http.ListenAndServe(":8080", s.Router))
}

func (s *server) Download(w http.ResponseWriter, r *http.Request) {
	id := s.nextID + int(time.Now().UnixNano())
	dirName := fmt.Sprintf("%s/%d", DIRECTORY_ROOT, id)
	rb, err := s.decodeRequestBody(r)
	if err != nil {
		s.respondWithError(w, 501, err.Error())
		return
	}

	req := Request{
		dirName: dirName,
		id:      strconv.Itoa(id),
		body:    rb,
	}
	s.nextID += 1

	if err := s.app.Download(req); err != nil {
		s.respondWithError(w, 501, err.Error())
		return
	}
	s.respondWithJSON(w, 200, map[string]int{"request_id": id})
}

func (s *server) Get(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	dirName := fmt.Sprintf("%s/%s", DIRECTORY_ROOT, id)
	req := Request{
		dirName: dirName,
		id:      id,
	}
	status, err := s.app.Get(req)
	if err != nil {
		s.respondWithError(w, 404, err.Error())
		return
	}
	s.respondWithJSON(w, 200, map[string]string{"status": *status})
}

func (s *server) Delete(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	dirName := fmt.Sprintf("%s/%s", DIRECTORY_ROOT, id)
	req := Request{
		dirName: dirName,
		id:      id,
	}
	err := s.app.Delete(req)
	if err != nil {
		s.respondWithError(w, 404, err.Error())
		return
	}
	s.respondWithJSON(w, 200, "successfully deleted.")
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

func (s *server) decodeRequestBody(r *http.Request) (*RequestBody, error) {
	var rb RequestBody
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rb); err != nil {
		return nil, err
	}
	defer r.Body.Close()
	return &rb, nil
}

func (a App) Download(r Request) error {
	// creates a new folder with name requestID
	if _, err := os.Stat(r.dirName); !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(r.dirName, 0755); err != nil {
		return err
	}

	splitUrl := strings.Split(r.body.Url, "/")
	r.body.Orig_data_file_name = splitUrl[len(splitUrl)-1]
	m := Manifest{
		Url:                 r.body.Url,
		Orig_data_file_name: r.body.Orig_data_file_name,
		Status:              "pending",
		Submission_time:     time.Now().UnixNano(),
		Start_time:          0,
		End_time:            0,
	}
	if err := a.UpdateManifest(&m, r.dirName); err != nil {
		return err
	}

	go a.DownloadFile(r.dirName, &m)

	m.Start_time = time.Now().UnixNano()
	m.Status = "in progress"
	if err := a.UpdateManifest(&m, r.dirName); err != nil {
		return err
	}
	return nil
}

func (a App) DownloadFile(directory string, mf *Manifest) error {
	fileExt := getFileExtension(mf.Orig_data_file_name)
	filePath := fmt.Sprintf("%s/data.%s", directory, fileExt)
	// Get the data
	mf.Start_time = time.Now().UnixNano()

	resp, err := http.Get(mf.Url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	mf.Status = "complete"
	mf.End_time = time.Now().UnixNano()

	a.UpdateManifest(mf, directory)

	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func (a App) Get(r Request) (*string, error) {
	m, err := a.GetManifest(r.dirName)
	if err != nil {
		return nil, err
	}
	status := m.Status
	return &status, nil
}

func (a App) Delete(r Request) error {
	if _, err := os.Stat(r.dirName); os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(r.dirName); err != nil {
		return err
	}
	return nil
}

func (a App) UpdateManifest(m *Manifest, directory string) error {
	mfPath := fmt.Sprintf("%s/manifest.json", directory)
	jsonMf, err := json.MarshalIndent(m, "", "	")
	if err != nil {
		return fmt.Errorf("%s", err.Error())
	}
	bs := []byte(jsonMf)
	ioutil.WriteFile(mfPath, bs, 0755)
	return nil
}

func (a App) GetManifest(directory string) (*Manifest, error) {
	mfPath := fmt.Sprintf("%s/manifest.json", directory)
	jsonFile, err := os.Open(mfPath)
	if err != nil {
		return nil, err
	}
	defer jsonFile.Close()
	mf, _ := ioutil.ReadAll(jsonFile)
	var m Manifest
	if err := json.Unmarshal(mf, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func getFileExtension(fileName string) string {
	return strings.Split(fileName, ".")[1]
}

// ------------- TO-DO ----------------- \\

// fix errors (error struct? use http response format?) switch statement errors?			respondWithJSON(w, http.StatusOK, p)
// write tests and make sure all working
// get concurrency working properly

// ------------- TO-DO ----------------- \\

/*

func TestUpdateProduct(t *testing.T) {

    clearTable()
    addProducts(1)

    req, _ := http.NewRequest("GET", "/product/1", nil)
    response := executeRequest(req)
    var originalProduct map[string]interface{}
    json.Unmarshal(response.Body.Bytes(), &originalProduct)

    var jsonStr = []byte(`{"name":"test product - updated name", "price": 11.22}`)
    req, _ = http.NewRequest("PUT", "/product/1", bytes.NewBuffer(jsonStr))
    req.Header.Set("Content-Type", "application/json")

    response = executeRequest(req)

    checkResponseCode(t, http.StatusOK, response.Code)

    var m map[string]interface{}
    json.Unmarshal(response.Body.Bytes(), &m)

    if m["id"] != originalProduct["id"] {
        t.Errorf("Expected the id to remain the same (%v). Got %v", originalProduct["id"], m["id"])
    }

    if m["name"] == originalProduct["name"] {
        t.Errorf("Expected the name to change from '%v' to '%v'. Got '%v'", originalProduct["name"], m["name"], m["name"])
    }

    if m["price"] == originalProduct["price"] {
        t.Errorf("Expected the price to change from '%v' to '%v'. Got '%v'", originalProduct["price"], m["price"], m["price"])
    }
}

*/
