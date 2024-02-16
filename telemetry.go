package main

import (
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tm "github.com/buger/goterm"
	cc "github.com/crazy3lf/colorconv"
)

type Telemetry struct {
	downloaders         *DownloaderCluster
	requests            *ResourceRequestList
	resources           *[]*Resource
	segments            *[]*ResourceSegment
	downloadersIndexMap map[*Downloader]int
	// clientIpMap map[*DownloaderClient]string
	resourceColorMap   map[*Resource]float64
	totalContentLength uint64
	chunkSize          uint64
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
	tel.downloadersIndexMap = make(map[*Downloader]int)
	tel.resourceColorMap = make(map[*Resource]float64)
	tel.totalContentLength = requests.TotalContentLength()
	tel.chunkSize = tel.totalContentLength / uint64(len(*downloaders))

	for i, d := range *downloaders {
		tel.downloadersIndexMap[&d] = i
	}

	n := 0
	for _, r := range *resources {
		tel.resourceColorMap[r] = float64(n)
		n = (n + 17) % 360
	}

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

	go func() {
		for {
			tel.Update()
			time.Sleep(50 * time.Millisecond)
		}
	}()
}

func (tel *Telemetry) Update() {
	tm.Clear()
	tm.MoveCursor(1, 1)

	tm.Print("Resources: ")

	screenWdith := tm.Width()
	usableWidth := uint(screenWdith-11) - 3
	// usableWidth := uint(30)

	for _, r := range *tel.resources {
		rss := append([]*ResourceSegment{}, r._segments...)
		rss = append(rss, r._writtenSegments...)
		sort.Slice(rss, func(i, j int) bool {
			return rss[i].from < rss[j].from
		})
		for _, rs := range rss {
			tel.PrintResourceSegmentProgress(rs, usableWidth)
		}
	}

	tm.Flush()
}

func (tel *Telemetry) GetDownloaderIndex(dwn *Downloader) int {
	return tel.downloadersIndexMap[dwn]
}

func (tel *Telemetry) PrintResourceSegmentProgress(rs *ResourceSegment, usableWidth uint) {
	rColor := tel.resourceColorMap[rs.resource]

	fr, fg, fb, _ := cc.HSVToRGB(rColor, 1, 1)
	br, bg, bb, _ := cc.HSVToRGB(rColor, 1, 0.5)

	pct := float64(rs.ContentLength()) / float64(tel.totalContentLength)
	barWidth := int(math.Round(float64(usableWidth) * pct))
	dwnProgress := min(rs.ack-rs.from, rs.ContentLength())
	filledWidth := int(math.Ceil(float64(dwnProgress) / float64(rs.ContentLength()) * float64(barWidth)))
	unfilledWidth := max(barWidth-filledWidth, 0)

	tm.Print(tm.BackgroundRGB(strings.Repeat(" ", filledWidth), fr, fg, fb))
	tm.Print(tm.BackgroundRGB(strings.Repeat(" ", unfilledWidth), br, bg, bb))
}
