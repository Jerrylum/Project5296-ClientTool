package main

import (
	"flag"
	"fmt"
	"math"
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

func DownloadResources(downloaders DownloaderCluster, requests ResourceRequestList) []*Resource {
	if len(downloaders) == 0 {
		panic("No downloader provided")
	}

	/////////////////////////
	/// Calculate the chunk size for each downloader
	/////////////////////////

	chunkSize := uint64(math.Ceil(float64(requests.TotalContentLength()) / float64(len(downloaders))))

	/////////////////////////
	/// Create resources and split them into segments
	/////////////////////////

	resources := requests.ToResources(chunkSize)

	/////////////////////////
	/// Sort the segments by the size from largest to smallest
	/////////////////////////

	segments := []*ResourceSegment{}

	for _, resource := range resources {
		segments = append(segments, resource._segments...)
	}

	// We want to download the largest segments first to better balance the load among the downloaders
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].ContentLength() > segments[j].ContentLength()
	})

	/////////////////////////
	/// Download the segments
	/////////////////////////

	telemetry.Start(&downloaders, &requests, &resources, &segments)
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
	logFilePathRaw := flag.String("log", "", "The path to the log file. If not provided, the log will be discarded.")

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

	if len(downloaders) == 0 {
		fmt.Println("No downloader available")
		os.Exit(1)
	}

	if len(availableRR) == 0 {
		fmt.Println("No resource to download")
		os.Exit(1)
	}

	telemetry.Init(*logFilePathRaw)

	// fmt.Println(availableRR)

	DownloadResources(downloaders, availableRR)

	telemetry.Update()
}
