package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Downloader struct {
	client *http.Client
}

type UserRequest struct {
	url  string
	dest string
}

type Resource struct {
	url           string
	destPath      string
	status        ResourceStatus
	contentLength int // in bytes
	isAcceptRange bool
}

type ResourceStatus int // 0: not started, 1: downloading, 2: downloaded, 3: failed
const (
	AVAILABLE ResourceStatus = iota
	DOWNLOADING
	DOWNLOADED
	NOT_AVAILABLE
	CONNECTION_TIMEOUT
	CONNECTION_REFUSED
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

func ConstructDownloadersFromIpList(ipList []string, numOfConn int) []Downloader {
	if len(ipList) == 0 || numOfConn <= 0 {
		panic("No proxy server or invalid number of connections provided")
	}

	var downloaders []Downloader
	var i = numOfConn

	for {
		for _, ip := range ipList {
			downloaders = append(downloaders, ConstructDownloaderFromIp(ip))
			i--

			if i == 0 {
				return downloaders
			}
		}
	}
}

func ConstructUserRequestsFromStringList(requests []string) []UserRequest {
	var userRequests []UserRequest
	for _, request := range requests {
		rawUrl := ""
		rawDest := ""
		if strings.Contains(request, " > ") {
			split := strings.Split(request, " > ")

			rawUrl = strings.TrimSpace(split[0])
			rawDest, _ = filepath.Abs(strings.TrimSpace(split[1]))
		} else {
			rawUrl = strings.TrimSpace(request)
			rawDest, _ = filepath.Abs("")
		}

		rawUrlWithoutFragment, _, _ := strings.Cut(rawUrl, "#")
		urlObj, err := url.ParseRequestURI(rawUrlWithoutFragment)
		if err != nil {
			panic("Error due to parsing url: " + request)
		}
		url := urlObj.String()
		urlFileName := path.Base(url)

		dest := ""
		info1, err2 := os.Stat(rawDest)
		if err2 == nil && info1.IsDir() == false {
			dest = rawDest // overwrite the destination
		} else if err2 == nil && info1.IsDir() {
			dest = path.Join(rawDest, urlFileName)
		} else {
			rawDestParent := path.Dir(rawDest)
			err3 := os.MkdirAll(rawDestParent, os.ModePerm)
			if err3 != nil {
				panic("Error due to creating directory: " + rawDestParent)
			}
			dest = rawDest
		}

		userRequests = append(userRequests, UserRequest{url: url, dest: dest})
	}

	return userRequests
}

func ConstructDownloaderFromIp(ip string) Downloader {
	url_i := url.URL{}
	url_proxy, _ := url_i.Parse("http://" + ip + ":3000")

	transport := &http.Transport{}
	transport.Proxy = http.ProxyURL(url_proxy)                        // set proxy
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // set ssl

	client := &http.Client{}
	client.Transport = transport
	client.Timeout = time.Second * 2

	return Downloader{client: client}
}

func ConstructResources(downloaders []Downloader, requestList []UserRequest) []Resource {
	resources := make([]Resource, len(requestList))

	jobs := make([]func(worker *Downloader), len(requestList))
	for i, request := range requestList {
		handleI := i
		handleRequest := request
		jobs[i] = func(downloader *Downloader) {
			// fmt.Println("Downloading", handleUrl, handleI)
			resources[handleI] = ConstructResourceFromURL(downloader, handleRequest)
		}
	}

	ConsumeJobs(downloaders, jobs)

	return resources
}

func ConstructResourceFromURL(downloader *Downloader, request UserRequest) Resource {
	client := downloader.client
	req, err := http.NewRequest("HEAD", request.url, nil)

	if err != nil {
		panic(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		errReason := err.Error()
		if strings.HasSuffix(errReason, "context deadline exceeded (Client.Timeout exceeded while awaiting headers)") {
			return Resource{
				url:           request.url,
				destPath:      request.dest,
				status:        CONNECTION_TIMEOUT,
				contentLength: 0,
				isAcceptRange: false}
		}
		if strings.HasSuffix(errReason, "connect: connection refused") {
			return Resource{
				url:           request.url,
				destPath:      request.dest,
				status:        CONNECTION_REFUSED,
				contentLength: 0,
				isAcceptRange: false}
		}
		panic(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return Resource{
			url:           request.url,
			destPath:      request.dest,
			status:        AVAILABLE,
			contentLength: int(resp.ContentLength),
			isAcceptRange: resp.Header.Get("Accept-Ranges") == "bytes"}
	} else {
		return Resource{
			url:           request.url,
			destPath:      request.dest,
			status:        NOT_AVAILABLE,
			contentLength: 0,
			isAcceptRange: false}
	}
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

func DownloadResources(downloaders []Downloader, resources []Resource) {
	// check := make(chan bool)
	// started := 0

	type LiveDownloader struct {
		entity    *Downloader
		isWorking bool
	}

	type ResourceSegment struct {
		resource *Resource
	}
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

	proxyList := ReadFileByLine(*proxyListPathRaw)
	requestList := ConstructUserRequestsFromStringList(ReadFileByLine(*requestListPathRaw))

	numOfConn := *numOfConnRaw
	downloaders := ConstructDownloadersFromIpList(proxyList, numOfConn)
	resources := ConstructResources(downloaders, requestList)

	fmt.Println(resources)

	// f, err := os.OpenFile("/home/ubuntu/client/output", os.O_RDWR|os.O_CREATE, 0600)
	// if err != nil {
	// 	panic(err)
	// }

	// // string to bytes
	// b := []byte("hello world     ") // 16 bytes
	// // f.Write(b)
	// f.WriteAt(b, 1024*1024*1024)

	// defer f.Close()

}
