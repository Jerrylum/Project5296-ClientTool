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
	"sort"
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

type ResourceRequest struct {
	url           string
	dest          string
	contentLength uint64 // in bytes
	isAcceptRange bool
	status        ResourceRequestStatus
}

type ResourceRequestStatus int

const (
	AVAILABLE ResourceRequestStatus = iota
	NOT_FOUND
	CONNECTION_TIMEOUT
	CONNECTION_REFUSED
)

type DownloadStatus int

type Resource struct {
	url              string
	dest             string
	contentLength    uint64 // in bytes
	isAcceptRange    bool
	_fd              *os.File
	_segments        []ResourceSegment
	_writtenSegments []ResourceSegment
}

type ResourceSegment struct {
	resource *Resource
	from     uint64 // inclusive
	to       uint64 // exclusive
	_status  DownloadStatus
}

const (
	PENDING DownloadStatus = iota
	DOWNLOADING
	DOWNLOADED
)

func (r *Resource) AddSegment(from uint64, to uint64) ResourceSegment {
	segment := ResourceSegment{resource: r, from: from, to: to, _status: PENDING}
	r._segments = append(r._segments, segment)
	return segment
}

func (r *Resource) OpenFile() error {
	if r._fd != nil {
		return nil
	}

	f, err := os.OpenFile(r.dest, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}

	r._fd = f
	return nil
}

func (r *Resource) CloseFile() error {
	if r._fd == nil {
		return nil
	}

	err := r._fd.Close()
	if err != nil {
		return err
	}

	r._fd = nil
	return nil
}

func (r *Resource) Status() DownloadStatus {
	if len(r._segments) == 0 {
		return DOWNLOADED
	}

	for _, seg := range r._segments {
		if seg._status == DOWNLOADING {
			return DOWNLOADING
		}
	}

	return PENDING
}

func (r *Resource) WriteAt(b []byte, off int64) (n int, err error) {
	if r._fd == nil {
		return 0, fmt.Errorf("The file is not opened")
	}
	return r._fd.WriteAt(b, off)
}

func (rs *ResourceSegment) WriteAt(b []byte, off int64) (n int, err error) {
	if rs._status != DOWNLOADING {
		return 0, fmt.Errorf("The segment is not downloading")
	}
	return rs.resource.WriteAt(b, off)
}

func (rs *ResourceSegment) ContentLength() uint64 {
	return rs.to - rs.from
}

func (rs *ResourceSegment) Status() DownloadStatus {
	return rs._status
}

func (rs *ResourceSegment) StartDownload() {
	if rs._status != PENDING {
		panic("The segment is not pending")
	}
	rs._status = DOWNLOADING

	if err := rs.resource.OpenFile(); err != nil {
		panic(err)
	}
}

func (rs *ResourceSegment) FinishDownload() {
	if rs._status != DOWNLOADING {
		panic("The segment is not downloading")
	}
	rs._status = DOWNLOADED

	// remove from _segments in resource
	for i, seg := range rs.resource._segments {
		if seg == *rs {
			rs.resource._segments = append(rs.resource._segments[:i], rs.resource._segments[i+1:]...)
			break
		}
	}

	// append to _writtenSegments in resource
	rs.resource._writtenSegments = append(rs.resource._writtenSegments, *rs)

	// if all segments are downloaded, close the file
	if len(rs.resource._segments) == 0 {
		rs.resource.CloseFile()
	}
}

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

func ConstructResourceRequests(downloaders []Downloader, userRequestList []UserRequest) []ResourceRequest {
	resourceRequests := make([]ResourceRequest, len(userRequestList))

	jobs := make([]func(worker *Downloader), len(userRequestList))
	for i, request := range userRequestList {
		handleI := i
		handleRequest := request
		jobs[i] = func(downloader *Downloader) {
			// fmt.Println("Downloading", handleUrl, handleI)
			resourceRequests[handleI] = ConstructResourceRequestFromUserRequest(downloader, handleRequest)
		}
	}

	ConsumeJobs(downloaders, jobs)

	return resourceRequests
}

func ConstructResourceRequestFromUserRequest(downloader *Downloader, request UserRequest) ResourceRequest {
	client := downloader.client
	req, err := http.NewRequest("HEAD", request.url, nil)

	if err != nil {
		panic(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		errReason := err.Error()
		if strings.HasSuffix(errReason, "context deadline exceeded (Client.Timeout exceeded while awaiting headers)") {
			return ResourceRequest{
				url:           request.url,
				dest:          request.dest,
				status:        CONNECTION_TIMEOUT,
				contentLength: 0,
				isAcceptRange: false}
		}
		if strings.HasSuffix(errReason, "connect: connection refused") {
			return ResourceRequest{
				url:           request.url,
				dest:          request.dest,
				status:        CONNECTION_REFUSED,
				contentLength: 0,
				isAcceptRange: false}
		}
		panic(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return ResourceRequest{
			url:           request.url,
			dest:          request.dest,
			status:        AVAILABLE,
			contentLength: uint64(resp.ContentLength), // XXX: validate the data
			isAcceptRange: resp.Header.Get("Accept-Ranges") == "bytes"}
	} else {
		return ResourceRequest{
			url:           request.url,
			dest:          request.dest,
			status:        NOT_FOUND,
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

func DownloadResources(downloaders []Downloader, requests []ResourceRequest) {
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

	var resources []Resource
	var segments []ResourceSegment
	for _, request := range requests {
		resource := Resource{
			url:              request.url,
			dest:             request.dest,
			contentLength:    request.contentLength,
			isAcceptRange:    request.isAcceptRange,
			_fd:              nil,
			_segments:        []ResourceSegment{},
			_writtenSegments: []ResourceSegment{}}
		resources = append(resources, resource)

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

	check := make(chan bool)
	started := 0

	type LiveDownloader struct {
		entity    *Downloader
		isWorking bool
	}

	liveDownloaders := make([]LiveDownloader, len(downloaders))
	for i, downloader := range downloaders {
		liveDownloaders[i] = LiveDownloader{entity: &downloader, isWorking: false}
	}

	pendingQueue := make(chan *ResourceSegment, len(segments))
	for _, seg := range segments {
		putSeg := seg
		pendingQueue <- &putSeg
	}

	for {
		for _, ld := range liveDownloaders {
			if !ld.isWorking {
				go func(ld2 *LiveDownloader) {
					ld2.isWorking = true
					seg := <-pendingQueue
					seg.StartDownload()
					started++
					check <- true
					// TODO
					seg.FinishDownload()
					ld2.isWorking = false
				}(&ld)
			}
		}

		<-check
		if started == len(segments) {
			break
		}
	}

	/////////////////////////
	/// TODO
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
	rrs := ConstructResourceRequests(downloaders, requestList)

	availableRR := []ResourceRequest{}
	for _, rr := range rrs {
		if rr.status == AVAILABLE {
			availableRR = append(availableRR, rr)
		}
	}

	fmt.Println(availableRR)

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
