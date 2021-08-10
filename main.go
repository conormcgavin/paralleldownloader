package main

import (
	"fmt"
	"os"

	"github.com/conormcgavin/resourceDownloader/downloader"
)

func main() {
	_, err := os.Create("downloader/requests/0/data.png")
	if err != nil {
		fmt.Println(err.Error())
	}
	server, err := downloader.NewServer("downloader/requests")
	if err != nil {
		fmt.Println(err)
	}
	server.HandleRequests()
	server.Run()
}
