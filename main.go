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

func IsAllResourceRequestAvailable(requests ResourceRequestList) bool {
	for _, request := range requests {
		if request.status != AVAILABLE {
			return false
		}
	}

	return true
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
	allResourceRequests := downloaders.FetchResourceRequests(userRequests)

	/////////////////////////
	/// Check if all resources are available
	/////////////////////////

	resourceRequests := ResourceRequestList{}

	if len(downloaders) == 0 {
		fmt.Println("No downloader available")
		os.Exit(1)
	}

	if !IsAllResourceRequestAvailable(allResourceRequests) {
		fmt.Println("The following resources are not available.")

		for _, rr := range allResourceRequests {
			if rr.status == AVAILABLE {
				resourceRequests = append(resourceRequests, rr)
			} else {
				switch rr.status {
				case NOT_FOUND:
					fmt.Printf("Status code != 200: %s\n", rr.url)
				case CONNECTION_TIMEOUT:
					fmt.Printf("Connection timeout: %s\n", rr.url)
				case CONNECTION_REFUSED:
					fmt.Printf("Connection refused: %s\n", rr.url)
				}
			}
		}

		for {
			fmt.Print("Do you want to continue downloading the available resources (y/n)? ")
			input := ""
			fmt.Scanln(&input)
			if strings.ToLower(input) == "y" {
				break
			} else if strings.ToLower(input) == "n" {
				os.Exit(0)
			}
		}
	} else {
		resourceRequests = allResourceRequests
	}

	if len(resourceRequests) == 0 {
		fmt.Println("No resource to download")
		os.Exit(1)
	}

	/////////////////////////
	/// Init telemetry and start download process
	/////////////////////////

	telemetry.Init(*logFilePathRaw)

	/////////////////////////
	/// Calculate the chunk size for each downloader
	/////////////////////////

	chunkSize := uint64(math.Ceil(float64(resourceRequests.TotalContentLength()) / float64(len(downloaders))))

	/////////////////////////
	/// Create resources and split them into segments
	/////////////////////////

	resources := resourceRequests.ToResources(chunkSize)

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

	telemetry.Start(&downloaders, &resourceRequests, &resources, &segments)

	downloaders.Download(&segments)

	telemetry.Update()

	/////////////////////////
	/// Print the report
	/////////////////////////

	fmt.Println("\n\nDownload completed")

	telemetry.PrintReport()
}
