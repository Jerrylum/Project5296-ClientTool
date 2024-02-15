package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func ReadFileByLine(path string) []string {
	dat, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	var rtn []string
	for _, str := range strings.Split(string(dat), "\n") {
		if str != "" {
			rtn = append(rtn, str)
		}
	}

	return rtn
}

func ConsumeJobs[T any](workers []T, jobs []func(worker *T)) {
	check := make(chan bool)
	consumed := 0

	type LiveWorker struct {
		entity    *T
		isWorking bool
	}

	liveDownloaders := make([]LiveWorker, len(workers))
	for i, downloader := range workers {
		liveDownloaders[i] = LiveWorker{entity: &downloader, isWorking: false}
	}

	jobsQueue := make(chan *func(worker *T), len(jobs))
	for _, job := range jobs {
		putJob := job
		jobsQueue <- &putJob
	}

	for {
		for _, ld := range liveDownloaders {
			if !ld.isWorking {
				go func(ld2 *LiveWorker) {
					ld2.isWorking = true
					(*<-jobsQueue)(ld2.entity)
					ld2.isWorking = false
					consumed++
					check <- true
				}(&ld)
			}
		}

		<-check
		if consumed == len(jobs) {
			break
		}
	}
}

func DownloadResources(downloaders DownloaderCluster, requests []ResourceRequest) []*Resource {
	/////////////////////////
	/// Calculate the total size of the requests
	/////////////////////////

	totalSize := uint64(0) // in bytes
	for _, request := range requests {
		totalSize += request.contentLength
	}

	chunkSize := totalSize / uint64(len(downloaders))

	/////////////////////////
	/// Create resources and split them into segments
	/////////////////////////

	var resources []*Resource
	var segments []*ResourceSegment
	for _, request := range requests {
		resource := Resource{
			url:              request.url,
			dest:             request.dest,
			contentLength:    request.contentLength,
			isAcceptRange:    request.isAcceptRange,
			_fd:              nil,
			_segments:        []*ResourceSegment{},
			_writtenSegments: []*ResourceSegment{}}

		resources = append(resources, &resource)

		if request.isAcceptRange {
			for idx := uint64(0); idx < request.contentLength; {
				maxChunkSize := min(request.contentLength, idx+chunkSize)
				segment := resource.AddSegment(idx, maxChunkSize)
				segments = append(segments, segment)
				idx += maxChunkSize
			}
		} else {
			segment := resource.AddSegment(0, request.contentLength)
			segments = append(segments, segment)
		}
	}

	/////////////////////////
	/// Sort the segments by the size from largest to smallest
	/////////////////////////

	// We want to download the largest segments first to better balance the load among the downloaders
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].ContentLength() > segments[j].ContentLength()
	})

	/////////////////////////
	/// Consume the segments
	/////////////////////////

	downloaders.Download(segments)

	return resources
}

func main() {
	proxyListPathRaw := flag.String("proxies", "", "The path to a file with a list of proxy server ips, separated by linefeed")
	requestListPathRaw := flag.String("requests", "", `The path to a file with a list of download requests, separated by linefeed
Each line can be one of the following formats:
 - Only the URL
   e.g. 'http://example.com/file.zip'
   The file will be saved to the current directory with the same name as the file in the url
 - URL with existing directory
   e.g. 'http://example.com/file.zip > /path/to/save/'
   The file will be saved to the specified existing directory with the same name as the file in the url 
 - URL with specified file path
   e.g. 'http://example.com/file.zip > /path/to/save/file.zip'
   The file will be saved to the specified path
`)
	numOfConnRaw := flag.Int("connections", 0, "The number of connections in total to download")

	flag.Parse()

	if *proxyListPathRaw == "" && *requestListPathRaw == "" && *numOfConnRaw == 0 {
		flag.PrintDefaults()
		return
	}

	if *proxyListPathRaw == "" {
		fmt.Println("Please provide a list of proxy servers")
		os.Exit(1)
	}

	if *requestListPathRaw == "" {
		fmt.Println("Please provide a list of urls to download")
		os.Exit(1)
	}

	if *numOfConnRaw == 0 {
		fmt.Println("Please provide the number of connections")
		os.Exit(1)
	}

	if *numOfConnRaw < 0 {
		fmt.Println("The number of connections must be greater than 0")
		os.Exit(1)
	}

	proxyIps := IpList(ReadFileByLine(*proxyListPathRaw))
	originalUserRequests := OriginalUserRequestList(ReadFileByLine(*requestListPathRaw))

	numOfConn := *numOfConnRaw
	downloaders := proxyIps.ToDownloaderCluster(numOfConn)
	userRequests := originalUserRequests.ToUserRequests()
	resourceRequests := downloaders.FetchResourceRequests(userRequests)

	availableRR := []ResourceRequest{}
	for _, rr := range resourceRequests {
		if rr.status == AVAILABLE {
			availableRR = append(availableRR, rr)
		}
	}

	fmt.Println(availableRR)

	DownloadResources(downloaders, availableRR)
}
