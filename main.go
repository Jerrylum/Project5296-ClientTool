package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Job func(downloader *Downloader)

type Downloader struct {
	client *http.Client
}

type Resource struct {
	url           string
	contentLength int // in bytes
	destPath      string
	isAcceptRange bool
}

func readFileByLine(path string) []string {
	dat, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	return strings.Split(string(dat), "\n")
}

func constructDownloadersFromIpList(ipList []string, numOfConn int) []Downloader {
	if len(ipList) == 0 || numOfConn <= 0 {
		panic("Invalid input")
	}

	var downloaders []Downloader
	var i = numOfConn

	for {
		for _, ip := range ipList {
			downloaders = append(downloaders, constructDownloaderFromIp(ip))
			i--

			if i == 0 {
				return downloaders
			}
		}
	}
}

func constructDownloaderFromIp(ip string) Downloader {
	url_i := url.URL{}
	url_proxy, _ := url_i.Parse("http://" + ip + ":3000")

	transport := &http.Transport{}
	transport.Proxy = http.ProxyURL(url_proxy)                        // set proxy
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //set ssl

	client := &http.Client{}
	client.Transport = transport

	return Downloader{client: client}
}

func constructResources(downloaders []Downloader, urlList []string) []Resource {
	resources := make([]Resource, len(urlList))

	jobs := make([]Job, len(urlList))
	for i, url := range urlList {
		jobs[i] = func(downloader *Downloader) {
			resources[i] = constructResourceFromURL(downloader, url)
		}
	}

	consumeJobs(downloaders, jobs)

	return resources
}

func constructResourceFromURL(downloader *Downloader, url string) Resource {
	return Resource{url: url}
}

func consumeJobs(downloaders []Downloader, jobs []Job) {
	check := make(chan bool)
	consumed := 0

	type LiveDownloader struct {
		entity    *Downloader
		isWorking bool
	}

	liveDownloaders := make([]LiveDownloader, len(downloaders))
	for i, downloader := range downloaders {
		liveDownloaders[i] = LiveDownloader{entity: &downloader, isWorking: false}
	}

	jobsQueue := make(chan *Job, len(jobs))
	for _, job := range jobs {
		jobsQueue <- &job
	}

	for {
		for _, ld := range liveDownloaders {
			if !ld.isWorking {
				go func(ld2 *LiveDownloader) {
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

func main() {
	var proxyListPathRaw = flag.String("proxies", "", "The path to a file with a list of proxy server ips separated by linefeed")
	var urlListPathRaw = flag.String("urls", "", "The path to a file with a list of urls to download separated by linefeed")
	var numOfConnRaw = flag.Int("connections", 0, "The number of connections in total to download")

	flag.Parse()

	if *proxyListPathRaw == "" && *urlListPathRaw == "" && *numOfConnRaw == 0 {
		flag.PrintDefaults()
		return
	}

	if *proxyListPathRaw == "" {
		panic("Please provide a list of proxy servers")
	}

	if *urlListPathRaw == "" {
		panic("Please provide a list of urls to download")
	}

	if *numOfConnRaw == 0 {
		panic("Please provide the number of connections")
	} else if *numOfConnRaw < 0 {
		panic("The number of connections must be greater than 0")
	}

	var proxyList []string = readFileByLine(*proxyListPathRaw)
	var urlList []string = readFileByLine(*urlListPathRaw)

	var numOfConn = *numOfConnRaw
	var downloaders []Downloader = constructDownloadersFromIpList(proxyList, numOfConn)
	var resources []Resource = constructResources(downloaders, urlList)

	fmt.Println(resources)

}
