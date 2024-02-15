package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	tm "github.com/buger/goterm"
)

type Telemetry struct {
	downloaders *DownloaderCluster
	requests    *ResourceRequestList
	resources   *[]*Resource
	segments    *[]*ResourceSegment
}

var telemetry Telemetry

func (tel *Telemetry) Init(logFilePathRaw string) {
	if logFilePathRaw == "" {
		log.SetOutput(io.Discard)
	} else {
		os.MkdirAll(filepath.Dir(logFilePathRaw), os.ModePerm)
		f, err := os.OpenFile(logFilePathRaw, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatalf("error opening file: %v", err)
		}
		// defer f.Close()

		log.SetOutput(f)
	}

	tm.Clear()
	tm.Flush()
}

func (tel *Telemetry) Start(
	downloaders *DownloaderCluster,
	requests *ResourceRequestList,
	resources *[]*Resource,
	segments *[]*ResourceSegment) {
	tel.downloaders = downloaders
	tel.requests = requests
	tel.resources = resources
	tel.segments = segments
	// tm.Clear()
	// tm.MoveCursor(1, 1)
	// tm.Println("Downloaders:")
	// for _, d := range *tel.downloaders {
	// 	tm.Println(d)
	// }
	// tm.Println("Requests:")
	// for _, r := range *tel.requests {
	// 	tm.Println(r)
	// }
	// tm.Println("Resources:")
	// for _, r := range *tel.resources {
	// 	tm.Println(r)
	// }
	// tm.Flush()
}
