package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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

type DownloadResult int

const (
	CLIENT_RETURNED_ERROR DownloadResult = iota
	STATUS_CODE_NOT_2XX
	READER_RETURNED_ERROR
	READ_SUCCESS
)

type DownloaderClient interface {
	Do(req *http.Request, timeout time.Duration) (*http.Response, error)
}

type DownloaderClientImpl http.Client

func (client *DownloaderClientImpl) Do(req *http.Request, timeout time.Duration) (*http.Response, error) {
	client.Timeout = timeout
	return (*http.Client)(client).Do(req)
}

type Downloader struct {
	client DownloaderClient
}

func (dwn *Downloader) FetchResourceRequest(userRequest UserRequest) ResourceRequest {
	req, err := http.NewRequest("HEAD", userRequest.url, nil)

	if err != nil {
		panic(err)
	}

	resp, err := dwn.client.Do(req, time.Second*2) // TODO configurable timeout
	if err != nil {
		errReason := err.Error()
		if strings.HasSuffix(errReason, "context deadline exceeded (Client.Timeout exceeded while awaiting headers)") {
			return ResourceRequest{
				url:           userRequest.url,
				dest:          userRequest.dest,
				status:        CONNECTION_TIMEOUT,
				contentLength: 0,
				isAcceptRange: false}
		}
		if strings.HasSuffix(errReason, "connect: connection refused") {
			return ResourceRequest{
				url:           userRequest.url,
				dest:          userRequest.dest,
				status:        CONNECTION_REFUSED,
				contentLength: 0,
				isAcceptRange: false}
		}
		panic(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		return ResourceRequest{
			url:           userRequest.url,
			dest:          userRequest.dest,
			status:        AVAILABLE,
			contentLength: uint64(resp.ContentLength), // XXX: validate the data
			isAcceptRange: resp.Header.Get("Accept-Ranges") == "bytes"}
	} else {
		return ResourceRequest{
			url:           userRequest.url,
			dest:          userRequest.dest,
			status:        NOT_FOUND,
			contentLength: 0,
			isAcceptRange: false}
	}
}

func (dwn *Downloader) Download(seg *ResourceSegment) DownloadResult {
	telemetry.ReportDownloadingSegment(dwn, seg)

	seg.StartDownload()

	req, err := http.NewRequest("GET", seg.resource.url, nil)
	req.Header.Add("Range", "bytes="+fmt.Sprint(seg.from)+"-"+fmt.Sprint(seg.to-2))

	if err != nil {
		panic(err)
	}
	resp, err := dwn.client.Do(req, 0)

	if err != nil {
		log.Println("Download(*ResourceSegment) failed, status: CLIENT_RETURNED_ERROR url:", seg.resource.url, "error:", err) // TODO telemetry
		seg.CancelDownload()
		return CLIENT_RETURNED_ERROR
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		log.Println("Download(*ResourceSegment) failed, status: STATUS_CODE_NOT_2XX url:", seg.resource.url) // TODO telemetry
		seg.CancelDownload()
		return STATUS_CODE_NOT_2XX
	}

	buf := make([]byte, 1024*1024*10) // 10MB buffer
	seg.ack = seg.from
	for {
		n, err := resp.Body.Read(buf)

		if n > 0 {
			seg.WriteAt(buf[:n], int64(seg.ack))
			seg.ack += uint64(n)
		}

		if seg.ack >= seg.to {
			log.Println("Download(*ResourceSegment) break, status: READ_SUCCESS url:", seg.resource.url) // TODO telemetry
			seg.FinishDownload()
			return READ_SUCCESS
		}

		if err == io.EOF {
			log.Println("Download(*ResourceSegment) EOF, status: READ_SUCCESS url:", seg.resource.url) // TODO telemetry
			seg.FinishDownload()
			return READ_SUCCESS
		}

		if err != nil {
			log.Println("Download(*ResourceSegment) failed, status: READER_RETURNED_ERROR url:", seg.resource.url, "error:", err) // TODO telemetry
			seg.CancelDownload()
			return READER_RETURNED_ERROR
		}
	}
}

type DownloaderCluster []*Downloader

func (dc *DownloaderCluster) FetchResourceRequests(userRequests []UserRequest) ResourceRequestList {
	resourceRequests := make(ResourceRequestList, len(userRequests))

	jobs := make([]func(worker *Downloader), len(userRequests))
	for i, request := range userRequests {
		handleI := i
		handleRequest := request
		jobs[i] = func(downloader *Downloader) {
			// fmt.Println("Downloading", handleUrl, handleI)
			resourceRequests[handleI] = downloader.FetchResourceRequest(handleRequest)
		}
	}

	ConsumeJobs(*dc, jobs)

	return resourceRequests
}

func (dc *DownloaderCluster) Download(segments []*ResourceSegment) {
	waitingSplitSegList := ThreadSafeSortedList[ResourceSegment]{
		list: []*ResourceSegment{},
		less: func(i, j *ResourceSegment) bool {
			return i.ContentLength() > j.ContentLength()
		}}

	pendingSegQueue := make(chan *ResourceSegment, len(segments))
	for _, seg := range segments {
		putSeg := seg
		pendingSegQueue <- putSeg
	}
	downloaderQueue := make(chan *Downloader, len(*dc))
	for _, downloader := range *dc {
		putDownloader := downloader
		downloaderQueue <- putDownloader
	}

	for {
		dwn := <-downloaderQueue

		// break if all segments are downloaded or failed
		if IsAllSegmentsSettled(segments) {
			break
		}

		var seg *ResourceSegment = nil
		if len(pendingSegQueue) != 0 {
			seg = <-pendingSegQueue
		} else {
			for waitingSplitSegList.Len() != 0 {
				firstHalf := waitingSplitSegList.Pop()
				if !firstHalf.IsSettled() && firstHalf.to-firstHalf.ack > 1024 { // TODO configurable 1KB
					secondHalf := firstHalf.Split()
					log.Println("Split first from:", firstHalf.from, "to:", firstHalf.to, "; second from:", secondHalf.from, "to:", secondHalf.to) // TODO telemetry
					segments = append(segments, secondHalf)
					telemetry.ReportNewSegmentAdded(secondHalf)
					seg = secondHalf
					break
				}
			}

			if seg == nil {
				downloaderQueue <- dwn
				log.Println("Download([]*ResourceSegment) idle") // TODO telemetry
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		if seg.resource.isAcceptRange {
			waitingSplitSegList.Add(seg)
		}

		go func(dwn *Downloader, seg *ResourceSegment) {
			result := dwn.Download(seg)
			waitingSplitSegList.Remove(seg)

			if result == READ_SUCCESS {
				log.Println("Download([]*ResourceSegment) success, url:", seg.resource.url, "from:", seg.from, "to:", seg.to) // TODO telemetry
			} else {
				if seg.ttl > 0 {
					log.Println("Download([]*ResourceSegment) return to pending queue, url:", seg.resource.url, "from:", seg.from, "to:", seg.to, "ttl:", seg.ttl) // TODO telemetry
					pendingSegQueue <- seg
				} else {
					log.Println("Download([]*ResourceSegment) ttl = 0, url:", seg.resource.url, "from:", seg.from, "to:", seg.to) // TODO telemetry
				}
			}
			downloaderQueue <- dwn
		}(dwn, seg)
	}

	log.Println("Download([]*ResourceSegment) finished") // TODO telemetry
}

type IpList []string

func (ipList *IpList) ToDownloaderCluster(numOfConn int) DownloaderCluster {
	if len(*ipList) == 0 || numOfConn <= 0 {
		panic("No proxy server or invalid number of connections provided")
	}

	var downloaders []*Downloader
	var i = numOfConn

	for {
		for _, ip := range *ipList {
			downloaders = append(downloaders, ConstructDownloaderFromIp(ip))
			i--

			if i == 0 {
				return downloaders
			}
		}
	}
}

type OriginalUserRequestList []string

func (ourList *OriginalUserRequestList) ToUserRequests() []UserRequest {
	var userRequests []UserRequest
	for _, request := range *ourList {
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
		if err2 == nil && !info1.IsDir() {
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

type ResourceRequestList []ResourceRequest

func (rrl *ResourceRequestList) TotalContentLength() uint64 {
	totalSize := uint64(0) // in bytes
	for _, request := range *rrl {
		totalSize += request.contentLength
	}

	return totalSize
}

func (rrl *ResourceRequestList) ToResources(chunkSize uint64) []*Resource {
	var resources []*Resource
	for _, request := range *rrl {
		resource := Resource{
			url:              request.url,
			dest:             request.dest,
			contentLength:    request.contentLength,
			isAcceptRange:    request.isAcceptRange,
			_fd:              nil,
			_segments:        []*ResourceSegment{},
			_writtenSegments: []*ResourceSegment{}}

		resources = append(resources, &resource)
		resource.SliceSegments(chunkSize)
	}

	return resources
}

func ConstructDownloaderFromIp(ip string) *Downloader {
	url_i := url.URL{}
	url_proxy, _ := url_i.Parse("http://" + ip + ":3000")

	transport := &http.Transport{}
	transport.Proxy = http.ProxyURL(url_proxy)                        // set proxy
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // set ssl

	client := &DownloaderClientImpl{}
	client.Transport = transport

	return &Downloader{client: client}
}
