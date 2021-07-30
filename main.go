package main

import (
	"fmt"

	"github.com/conormcgavin/resourceDownloader/downloader"
)

func main() {
	server, err := downloader.NewServer()
	if err != nil {
		fmt.Println(err)
	}
	server.HandleRequests()
	server.Run()
}
