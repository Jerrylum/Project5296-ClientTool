package main

import (
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tm "github.com/buger/goterm"
	cc "github.com/crazy3lf/colorconv"
)

type TelemetryProgressBarColor struct {
	fr, fg, fb, br, bg, bb uint8
}

type Telemetry struct {
	downloaders         *DownloaderCluster
	requests            *ResourceRequestList
	resources           *[]*Resource
	segments            *[]*ResourceSegment
	downloadersIndexMap map[*Downloader]int
	// clientIpMap map[*DownloaderClient]string
	resourceColorMap        map[*Resource]float64
	resourceIdMap           map[*Resource]uint
	segmentIdMap            map[*ResourceSegment]uint
	resourceSegmentCountMap map[*Resource]uint
	clientSegmentMapMutex   *sync.Mutex
	clientSegmentMap        map[*Downloader]*ResourceSegment
	totalContentLength      uint64
	chunkSize               uint64
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
	tel.resourceIdMap = make(map[*Resource]uint)
	tel.segmentIdMap = make(map[*ResourceSegment]uint)
	tel.resourceSegmentCountMap = make(map[*Resource]uint)
	tel.clientSegmentMapMutex = &sync.Mutex{}
	tel.clientSegmentMapMutex.Lock()
	tel.clientSegmentMap = make(map[*Downloader]*ResourceSegment)
	defer tel.clientSegmentMapMutex.Unlock()
	tel.totalContentLength = requests.TotalContentLength()
	tel.chunkSize = uint64(math.Ceil(float64(tel.totalContentLength) / float64(len(*downloaders))))

	for i, d := range *downloaders {
		tel.downloadersIndexMap[d] = i
	}

	n := 0
	idx := uint(0)
	for _, r := range *resources {
		tel.resourceColorMap[r] = float64(n)
		tel.resourceIdMap[r] = idx
		n = (n + 17) % 360
		idx++
	}

	for _, rs := range *segments {
		tel.segmentIdMap[rs] = tel.resourceSegmentCountMap[rs.resource]
		tel.resourceSegmentCountMap[rs.resource]++
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
	usableWidth := uint(screenWdith-11) - 2

	for _, r := range *tel.resources {
		tel.PrintResourceProgress(r, usableWidth)
	}

	tm.MoveCursor(1, 3)
	tm.Print("Downloaders: ")
	for i, dwn := range *tel.downloaders {
		tm.MoveCursor(1, i+4)
		tm.Printf("Downloader %2d - ", i)

		tel.clientSegmentMapMutex.Lock()
		rs := tel.clientSegmentMap[dwn]
		tel.clientSegmentMapMutex.Unlock()
		if rs != nil {
			r := rs.resource
			rColor := tel.resourceColorMap[r]

			fr, fg, fb, _ := cc.HSVToRGB(rColor, 1, 1)
			br, bg, bb, _ := cc.HSVToRGB(rColor, 1, 0.5)

			// resourceBarWidth := int(math.Round(float64(usableWidth) * float64(r.contentLength) / float64(tel.totalContentLength)))
			resourceBarWidth := usableWidth / 2

			pct := float64(rs.ContentLength()) / float64(r.contentLength)
			barWidth := int(math.Round(float64(resourceBarWidth) * pct))
			dwnProgress := min(rs.ack-rs.from, rs.ContentLength())
			filledWidth := int(math.Ceil(float64(dwnProgress) / float64(rs.ContentLength()) * float64(barWidth)))
			unfilledWidth := max(barWidth-filledWidth, 0)

			tm.Print(tm.BackgroundRGB(strings.Repeat(" ", filledWidth), fr, fg, fb))
			tm.Print(tm.BackgroundRGB(strings.Repeat(" ", unfilledWidth), br, bg, bb))
		} else {
			// tm.Print("Idle")
			tm.Printf("Idle %d", len((tel.clientSegmentMap)))

		}

	}

	tm.Flush()
}

func (tel *Telemetry) GetDownloaderIndex(dwn *Downloader) int {
	return tel.downloadersIndexMap[dwn]
}

func (tel *Telemetry) ReportNewSegmentAdded(rs *ResourceSegment) {
	tel.segmentIdMap[rs] = tel.resourceSegmentCountMap[rs.resource]
	tel.resourceSegmentCountMap[rs.resource]++
}

func (tel *Telemetry) PrintResourceProgress(r *Resource, usableWidth uint) {
	rss := append([]*ResourceSegment{}, r._segments...)
	rss = append(rss, r._writtenSegments...)
	sort.Slice(rss, func(i, j int) bool {
		return rss[i].from < rss[j].from
	})
	// rColor := tel.resourceColorMap[r]

	// fr, fg, fb, _ := cc.HSVToRGB(rColor, 1, 1)
	// br, bg, bb, _ := cc.HSVToRGB(rColor, 1, 0.5)
	color := GetTelemetryProgressBarColor(tel.resourceColorMap[r])

	resourceBarWidth := uint(math.Round(float64(usableWidth) * float64(r.contentLength) / float64(tel.totalContentLength)))

	for _, rs := range rss {
		// pct := float64(rs.ContentLength()) / float64(r.contentLength)
		// barWidth := int(math.Round(float64(resourceBarWidth) * pct))
		// dwnProgress := min(rs.ack-rs.from, rs.ContentLength())
		// filledWidth := int(math.Ceil(float64(dwnProgress) / float64(rs.ContentLength()) * float64(barWidth)))
		// unfilledWidth := max(barWidth-filledWidth, 0)

		// tm.Print(tm.BackgroundRGB(strings.Repeat(" ", filledWidth), fr, fg, fb))
		// tm.Print(tm.BackgroundRGB(strings.Repeat(" ", unfilledWidth), br, bg, bb))
		tel.PrintResourceSegmentProgress(rs, &color, uint(resourceBarWidth))
	}
}

func (tel *Telemetry) ReportDownloadingSegment(dwn *Downloader, rs *ResourceSegment) {
	tel.clientSegmentMapMutex.Lock()
	defer tel.clientSegmentMapMutex.Unlock()
	tel.clientSegmentMap[dwn] = rs
}

func GetTelemetryProgressBarColor(themeColor float64) TelemetryProgressBarColor {
	fr, fg, fb, _ := cc.HSVToRGB(themeColor, 1, 1)
	br, bg, bb, _ := cc.HSVToRGB(themeColor, 1, 0.5)
	return TelemetryProgressBarColor{
		fr: fr,
		fg: fg,
		fb: fb,
		br: br,
		bg: bg,
		bb: bb}
}

func (tel *Telemetry) PrintResourceSegmentProgress(rs *ResourceSegment, color *TelemetryProgressBarColor, resourceBarWidth uint) {
	idStr := fmt.Sprintf("%d-%d", tel.resourceIdMap[rs.resource], tel.segmentIdMap[rs])

	r := rs.resource
	pct := float64(rs.ContentLength()) / float64(r.contentLength)
	barWidth := int(math.Round(float64(resourceBarWidth) * pct))
	dwnProgress := min(rs.ack-rs.from, rs.ContentLength())
	filledWidth := int(math.Ceil(float64(dwnProgress) / float64(rs.ContentLength()) * float64(barWidth)))
	unfilledWidth := max(barWidth-filledWidth, 0)

	if barWidth > len(idStr) {
		if filledWidth != 0 {
			filledPart := fmt.Sprintf("%-"+strconv.Itoa(filledWidth)+"s", idStr)
			tm.Print(tm.BackgroundRGB(filledPart, color.fr, color.fg, color.fb))

		}
		if unfilledWidth != 0 {
			idStrPart2 := ""
			if filledWidth < len(idStr) {
				idStrPart2 = idStr[filledWidth:]
			}
			unfilledPart := fmt.Sprintf("%-"+strconv.Itoa(unfilledWidth)+"s", idStrPart2)
			tm.Print(tm.BackgroundRGB(unfilledPart, color.br, color.bg, color.bb))
		}
	} else {
		tm.Print(tm.BackgroundRGB(strings.Repeat(" ", filledWidth), color.fr, color.fg, color.fb))
		tm.Print(tm.BackgroundRGB(strings.Repeat(" ", unfilledWidth), color.br, color.bg, color.bb))
	}
}
