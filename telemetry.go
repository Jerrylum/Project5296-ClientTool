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

type TelemetryResourceSegmentRuntime struct {
	rs          *ResourceSegment
	ttl         uint8
	startTime   time.Time
	settledTime time.Time
}

type Telemetry struct {
	downloaders               *DownloaderCluster
	requests                  *ResourceRequestList
	resources                 *[]*Resource
	segments                  *[]*ResourceSegment
	downloadersIndexMap       map[*Downloader]int
	resourceColorMap          map[*Resource]float64
	resourceIdMap             map[*Resource]uint
	segmentIdMap              map[*ResourceSegment]uint
	resourceSegmentCountMap   map[*Resource]uint
	downloaderSegmentMapMutex *sync.Mutex
	downloaderSegmentMap      map[*Downloader][]*TelemetryResourceSegmentRuntime
	totalContentLength        uint64
	chunkSize                 uint64
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
	tel.downloaderSegmentMapMutex = &sync.Mutex{}
	tel.downloaderSegmentMapMutex.Lock()
	tel.downloaderSegmentMap = make(map[*Downloader][]*TelemetryResourceSegmentRuntime)
	defer tel.downloaderSegmentMapMutex.Unlock()
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
		tm.Printf("#%-2d - ", i)

		tel.downloaderSegmentMapMutex.Lock()

		arr := tel.downloaderSegmentMap[dwn]
		for _, runtime := range arr {
			rs := runtime.rs
			color := GetTelemetryProgressBarColor(tel.resourceColorMap[rs.resource])
			r := rs.resource

			resourceBarWidth := uint(math.Round(float64(usableWidth) * float64(r.contentLength) / float64(tel.totalContentLength)))
			tel.PrintResourceSegmentProgress(rs, &color, resourceBarWidth)
		}

		tel.downloaderSegmentMapMutex.Unlock()

		if len(arr) != 0 {
			lastRs := arr[len(arr)-1].rs
			pct := float64(lastRs.ack-lastRs.from) / float64(lastRs.ContentLength())
			info := fmt.Sprintf("Downloading %d_%d (%.2f%%)", tel.resourceIdMap[lastRs.resource], tel.segmentIdMap[lastRs], pct*100)
			w := tm.Width()
			tm.MoveCursor(w-28, i+4)
			tm.Printf("%28s", info)
		}
	}

	tm.Flush()
}

func (tel *Telemetry) PrintReport() {
	fmt.Println()
	fmt.Println("# Report")
	fmt.Println()
	fmt.Println("## Downloaders")
	fmt.Println()
	for _, dwn := range *tel.downloaders {
		arr := tel.downloaderSegmentMap[dwn]
		dwnIndex := tel.downloadersIndexMap[dwn]
		fmt.Printf("### Downloader #%d \n", dwnIndex)
		fmt.Println("#### Segments")
		totalRecived := uint64(0)
		for _, runtime := range arr {
			rs := runtime.rs
			idStr := fmt.Sprintf("%d_%d", tel.resourceIdMap[rs.resource], tel.segmentIdMap[rs])
			pct := float64(rs.ack-rs.from) / float64(rs.ContentLength())
			fmt.Printf(" - Segment#%s range=%d~%d len=%d received=%d (%s %.2f%%)", idStr, rs.from, rs.to, rs.ContentLength(), rs.ack-rs.from, SignedInt(int64(rs.ack)-int64(rs.to)), pct*100)
			if rs.status == DOWNLOAD_FAILED {
				fmt.Printf(" FAILED (ttl=%d)", rs.ttl)
			}
			fmt.Println()

			totalRecived += rs.ack - rs.from
		}
		fmt.Println("#### Summary")
		fmt.Printf(" - Recived %d duty=%s\n", totalRecived, SignedInt(int64(totalRecived)-int64(tel.chunkSize)))
		fmt.Printf(" - Time used: %dms\n", arr[len(arr)-1].settledTime.Sub(arr[0].startTime).Milliseconds())
		fmt.Println()
	}
	fmt.Println()
}

func (tel *Telemetry) ReportNewSegmentAdded(rs *ResourceSegment) {
	tel.segmentIdMap[rs] = tel.resourceSegmentCountMap[rs.resource]
	tel.resourceSegmentCountMap[rs.resource]++
}

func (tel *Telemetry) ReportDownloadingSegment(dwn *Downloader, rs *ResourceSegment) {
	tel.downloaderSegmentMapMutex.Lock()
	defer tel.downloaderSegmentMapMutex.Unlock()

	if tel.downloaderSegmentMap[dwn] == nil {
		tel.downloaderSegmentMap[dwn] = []*TelemetryResourceSegmentRuntime{{rs: rs, ttl: rs.ttl, startTime: time.Now()}}
	} else {
		tel.downloaderSegmentMap[dwn] = append(tel.downloaderSegmentMap[dwn], &TelemetryResourceSegmentRuntime{rs: rs, ttl: rs.ttl, startTime: time.Now()})
	}
}

func (tel *Telemetry) ReportDownloadSettled(dwn *Downloader, rs *ResourceSegment) {
	tel.downloaderSegmentMapMutex.Lock()
	defer tel.downloaderSegmentMapMutex.Unlock()

	arr := tel.downloaderSegmentMap[dwn]
	for _, runtime := range arr {
		if runtime.rs == rs {
			// dereference and copy
			runtime.rs = &ResourceSegment{
				resource: rs.resource,
				from:     rs.from,
				to:       rs.to,
				ack:      rs.ack,
				ttl:      rs.ttl,
				status:   rs.status}
			runtime.settledTime = time.Now()
			break
		}
	}
}

func (tel *Telemetry) PrintResourceProgress(r *Resource, usableWidth uint) {
	rss := append([]*ResourceSegment{}, r._segments...)
	rss = append(rss, r._writtenSegments...)
	sort.Slice(rss, func(i, j int) bool {
		return rss[i].from < rss[j].from
	})
	color := GetTelemetryProgressBarColor(tel.resourceColorMap[r])

	resourceBarWidth := uint(math.Round(float64(usableWidth) * float64(r.contentLength) / float64(tel.totalContentLength)))

	for _, rs := range rss {
		tel.PrintResourceSegmentProgress(rs, &color, uint(resourceBarWidth))
	}
}

func GetTelemetryProgressBarColor(themeColor float64) TelemetryProgressBarColor {
	fr, fg, fb, _ := cc.HSVToRGB(themeColor, 1, 1)
	br, bg, bb, _ := cc.HSVToRGB(themeColor, 1, 0.2)
	return TelemetryProgressBarColor{
		fr: fr,
		fg: fg,
		fb: fb,
		br: br,
		bg: bg,
		bb: bb}
}

func (tel *Telemetry) PrintResourceSegmentProgress(rs *ResourceSegment, color *TelemetryProgressBarColor, resourceBarWidth uint) {
	idStr := fmt.Sprintf("%d_%d", tel.resourceIdMap[rs.resource], tel.segmentIdMap[rs])

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

func SignedInt[T ~int | ~int8 | ~int16 | ~int32 | ~int64 |
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](n T) string {
	if n < 0 {
		return strconv.Itoa(int(n))
	}
	return "+" + strconv.Itoa(int(n))
}
