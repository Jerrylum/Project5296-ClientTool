package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

type ResourceStatus int

type ResourceLike struct{}

type Resource struct {
	ResourceLike
	url              string
	dest             string
	contentLength    uint64 // in bytes
	isAcceptRange    bool
	_fd              *os.File
	_segments        []*ResourceSegment
	_writtenSegments []*ResourceSegment
}

type ResourceSegment struct {
	ResourceLike
	resource *Resource
	from     uint64 // inclusive
	to       uint64 // exclusive
	ttl      uint8
	_status  ResourceStatus
}

type ThreadSafeSortedList[T ResourceLike] struct {
	list  []T
	less  func(i, j T) bool
	mutex sync.Mutex
}

const (
	PENDING ResourceStatus = iota
	DOWNLOADING
	DOWNLOADED
	DOWNLOAD_FAILED
)

func (r *Resource) AddSegment(from uint64, to uint64) *ResourceSegment {
	segment := ResourceSegment{resource: r, from: from, to: to, ttl: 3, _status: PENDING}
	r._segments = append(r._segments, &segment)
	return &segment
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

/*
PENDING: All segments are pending
DOWNLOADING: At least one segment is downloading
DOWNLOADED: All segments are downloaded successfully
DOWNLOAD_FAILED: No segments are downloading/pending and at least one segment is downloaded unsuccessfully
*/
func (r *Resource) Status() ResourceStatus {
	// if all segments are pending
	isAllPending := true
	isAllDownloaded := true
	for _, seg := range r._segments {
		if seg._status != PENDING {
			isAllPending = false
		}
		if seg._status == DOWNLOADING {
			return DOWNLOADING
		}
		if seg._status != DOWNLOADED {
			isAllDownloaded = false
		}
	}

	if isAllPending && !isAllDownloaded {
		return PENDING
	}

	if isAllDownloaded && !isAllPending {
		return DOWNLOADED
	}

	return DOWNLOAD_FAILED
}

func (r *Resource) WriteAt(b []byte, off int64) (n int, err error) {
	if r._fd == nil {
		return 0, fmt.Errorf("The file is not opened")
	}
	return r._fd.WriteAt(b, off)
}

func (rs *ResourceSegment) ContentLength() uint64 {
	return rs.to - rs.from
}

func (rs *ResourceSegment) Status() ResourceStatus {
	return rs._status
}

func (rs *ResourceSegment) StartDownload() {
	if rs._status != PENDING {
		panic("The segment is not pending")
	}
	if rs.ttl == 0 {
		panic("The segment has no more ttl")
	}
	rs._status = DOWNLOADING

	if err := rs.resource.OpenFile(); err != nil {
		panic(err)
	}
}

func (rs *ResourceSegment) CancelDownload() {
	if rs._status != DOWNLOADING {
		panic("The segment is not downloading")
	}
	rs.ttl--
	if rs.ttl < 0 {
		panic("The segment has -1 ttl")
	}
	if rs.ttl == 0 {
		rs._status = DOWNLOAD_FAILED
	} else {
		rs._status = PENDING
	}
}

func (rs *ResourceSegment) FinishDownload() {
	if rs._status != DOWNLOADING {
		panic("The segment is not downloading")
	}
	rs._status = DOWNLOADED

	// remove from _segments in resource
	for i, seg := range rs.resource._segments {
		if seg == rs {
			rs.resource._segments = append(rs.resource._segments[:i], rs.resource._segments[i+1:]...)
			break
		}
	}

	// append to _writtenSegments in resource
	rs.resource._writtenSegments = append(rs.resource._writtenSegments, rs)

	// if all segments are downloaded, close the file
	if len(rs.resource._segments) == 0 {
		rs.resource.CloseFile()
	}
}

func (rs *ResourceSegment) WriteAt(b []byte, off int64) (n int, err error) {
	if rs._status != DOWNLOADING {
		return 0, fmt.Errorf("The segment is not downloading")
	}
	return rs.resource.WriteAt(b, off)
}

func (ls *ThreadSafeSortedList[T]) Add(item T) {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()

	// list.list = append(list.list, item)
	// sort.Slice(list.list, func(i, j int) bool {
	// 	return list.less(list.list[i], list.list[j])
	// })

	// Time complexity: O(n)
	for i, listItem := range ls.list {
		if ls.less(item, listItem) {
			ls.list = append(ls.list[:i], append([]T{item}, ls.list[i:]...)...)
			return
		}
	}
}

func (ls *ThreadSafeSortedList[T]) Remove(item T) bool {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()

	// Time complexity: O(n)
	for i, listItem := range ls.list {
		if listItem == item {
			ls.list = append(ls.list[:i], ls.list[i+1:]...)
			return true
		}
	}

	return false
}

func IsAllResourcesFinished(resources []*Resource) bool {
	for _, resource := range resources {
		if resource.Status() == DOWNLOADING || resource.Status() == PENDING {
			return false
		}
	}
	return true
}

func AddResourceSegmentToSortedList(segments []*ResourceSegment, segment *ResourceSegment) []*ResourceSegment {
	for i, seg := range segments { // Time complexity: O(n)
		if seg.ContentLength() < segment.ContentLength() {
			segments = append(segments[:i], append([]*ResourceSegment{segment}, segments[i:]...)...)
			return segments
		}
	}

	return append(segments, segment)
}

func RemoveResourceSegmentFromList(segments []*ResourceSegment, segment *ResourceSegment) []*ResourceSegment {
	for i, seg := range segments { // Time complexity: O(n)
		if seg == segment {
			return append(segments[:i], segments[i+1:]...)
		}
	}

	return segments
}

func SplitSegment(firstHalf *ResourceSegment) *ResourceSegment {
	r := firstHalf.resource
	middle := firstHalf.from + (firstHalf.to-firstHalf.from)/2
	end := firstHalf.to
	secondHalf := ResourceSegment{resource: r, from: middle, to: end, ttl: 3, _status: PENDING}
	firstHalf.to = middle
	r._segments = append(r._segments, &secondHalf)
	return &secondHalf
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
	client.Timeout = time.Second * 2
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

	var splittableSegments []*ResourceSegment
	splitSegmentMutex := sync.Mutex{}

	pendingSegQueue := make(chan *ResourceSegment, len(segments))
	for _, seg := range segments {
		putSeg := seg
		pendingSegQueue <- putSeg
	}
	downloaderQueue := make(chan *Downloader, len(downloaders))
	for _, downloader := range downloaders {
		putDownloader := downloader
		downloaderQueue <- &putDownloader
	}

	for {
		dwn := <-downloaderQueue

		// break if all resources are downloaded or failed
		if IsAllResourcesFinished(resources) {
			break
		}

		var seg *ResourceSegment
		if len(pendingSegQueue) != 0 {
			seg = <-pendingSegQueue
		} else if len(splittableSegments) != 0 {
			splitSegmentMutex.Lock()
			firstHalf := splittableSegments[0]
			splittableSegments = splittableSegments[1:] // pop first element
			splitSegmentMutex.Unlock()

			secondHalf := SplitSegment(firstHalf)
			segments = append(segments, secondHalf)
			seg = secondHalf
		} else {
			downloaderQueue <- dwn
			log.Println("DownloadResources idle") // TODO telemetry
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if seg.ContentLength() > 1024 { // TODO configurable 1KB
			splitSegmentMutex.Lock()
			splittableSegments = AddResourceSegmentToSortedList(splittableSegments, seg)
			splitSegmentMutex.Unlock()
		}

		go func(dwn *Downloader, seg *ResourceSegment) {
			result := DownloadResource(dwn, seg)
			splitSegmentMutex.Lock()
			splittableSegments = RemoveResourceSegmentFromList(splittableSegments, seg)
			splitSegmentMutex.Unlock()

			if result == READ_SUCCESS {
				log.Println("DownloadResources success, url:", seg.resource.url, "from:", seg.from, "to:", seg.to) // TODO telemetry
			} else {
				if seg.ttl > 0 {
					log.Println("DownloadResources return to pending queue, url:", seg.resource.url, "from:", seg.from, "to:", seg.to, "ttl:", seg.ttl) // TODO telemetry
					pendingSegQueue <- seg
				} else {
					log.Println("DownloadResources ttl = 0, url:", seg.resource.url, "from:", seg.from, "to:", seg.to) // TODO telemetry
				}
			}
			downloaderQueue <- dwn
		}(dwn, seg)
	}

	log.Println("DownloadResources finished") // TODO telemetry
}

type DownloadResult int

const (
	CLIENT_RETURNED_ERROR DownloadResult = iota
	STATUS_CODE_NOT_2XX
	READER_RETURNED_ERROR
	READ_SUCCESS
)

func DownloadResource(download *Downloader, seg *ResourceSegment) DownloadResult {
	seg.StartDownload()

	client := download.client
	client.Timeout = 0
	req, err := http.NewRequest("GET", seg.resource.url, nil)
	req.Header.Add("Range", "bytes="+fmt.Sprint(seg.from)+"-"+fmt.Sprint(seg.to-2))

	if err != nil {
		panic(err)
	}
	resp, err := client.Do(req)

	if err != nil {
		log.Println("DownloadResource failed, status: CLIENT_RETURNED_ERROR url:", seg.resource.url, "error:", err) // TODO telemetry
		seg.CancelDownload()
		return CLIENT_RETURNED_ERROR
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		log.Println("DownloadResource failed, status: STATUS_CODE_NOT_2XX url:", seg.resource.url) // TODO telemetry
		seg.CancelDownload()
		return STATUS_CODE_NOT_2XX
	}

	buf := make([]byte, 1024*1024*10) // 10MB buffer
	offset := seg.from
	for {
		n, err := resp.Body.Read(buf)

		if n > 0 {
			seg.WriteAt(buf[:n], int64(offset))
			offset += uint64(n)
			fmt.Println(n)
		}

		if offset >= seg.to {
			log.Println("DownloadResource break, status: READ_SUCCESS url:", seg.resource.url) // TODO telemetry
			seg.FinishDownload()
			return READ_SUCCESS
		}

		if err == io.EOF {
			log.Println("DownloadResource EOF, status: READ_SUCCESS url:", seg.resource.url) // TODO telemetry
			seg.FinishDownload()
			return READ_SUCCESS
		}

		if err != nil {
			log.Println("DownloadResource failed, status: READER_RETURNED_ERROR url:", seg.resource.url, "error:", err) // TODO telemetry
			seg.CancelDownload()
			return READER_RETURNED_ERROR
		}
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
	rrs := ConstructResourceRequests(downloaders, requestList)

	availableRR := []ResourceRequest{}
	for _, rr := range rrs {
		if rr.status == AVAILABLE {
			availableRR = append(availableRR, rr)
		}
	}

	fmt.Println(availableRR)

	DownloadResources(downloaders, availableRR)
}
