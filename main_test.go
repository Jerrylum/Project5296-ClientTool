package main

import (
	"path/filepath"
	"testing"
)

func TestToDownloadCluster(t *testing.T) {
	testIPstr := []string{"127.0.0.1", "127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"}

	// Test the general conversion of string slice to IpList
	testIPlist := IpList(testIPstr)

	if len(testIPlist) != len(testIPstr) {
		t.Errorf("Expected %d, got %d", len(testIPstr), len(testIPlist))
	}
	for i, ip := range testIPlist {
		if ip != testIPstr[i] {
			t.Errorf("Expected %s, got %s", testIPstr[i], ip)
		}
	}

	// number of conn is 4 but waste 1 ip, should give 4 downloaders
	numOfConn := 4
	downloaders := testIPlist.ToDownloaderCluster(numOfConn)
	if len(downloaders) != numOfConn {
		t.Errorf("Expected %d, got %d", numOfConn, len(downloaders))
	}

	numOfConn = 10
	downloaders = testIPlist.ToDownloaderCluster(numOfConn)
	if len(downloaders) != numOfConn {
		t.Errorf("Expected %d, got %d", numOfConn, len(downloaders))
	}
}

func TestToUserRequests(t *testing.T) {
	testUserRequestSpec := []string{"http://16.163.217.155/download/200.jpg",
		"http://16.163.217.155/download/200.jpg > 200_1.jpg",
		"http://16.163.217.155/download/200.jpg > ./etc/200_2.jpg"}

	testUserResuestResultUrl := []string{"http://16.163.217.155/download/200.jpg", "http://16.163.217.155/download/200.jpg", "http://16.163.217.155/download/200.jpg"}
	dest, _ := filepath.Abs("")
	testUserResuestResultDest := []string{dest + "/200.jpg", dest + "/200_1.jpg", dest + "/etc/200_2.jpg"}

	testUserRequestStr := OriginalUserRequestList(testUserRequestSpec)

	if len(testUserRequestStr) != len(testUserRequestSpec) {
		t.Errorf("Expected %d, got %d", len(testUserRequestSpec), len(testUserRequestStr))
	}

	testUserRequests := testUserRequestStr.ToUserRequests()
	if len(testUserRequests) != len(testUserRequestSpec) {
		t.Errorf("Expected %d, got %d", len(testUserRequestSpec), len(testUserRequests))
	}

	for i, userRequest := range testUserRequests {
		if userRequest.url != testUserResuestResultUrl[i] {
			t.Errorf("Expected %s, got %s", testUserResuestResultUrl[i], userRequest.url)
		}
		if userRequest.dest != testUserResuestResultDest[i] {
			t.Errorf("Expected %s, got %s", testUserResuestResultDest[i], userRequest.dest)
		}
	}
}

type DownloaderClusterMock DownloaderCluster

func (dc DownloaderClusterMock) FetchResourceRequests() ResourceRequestList {
	resourceRequests := make(ResourceRequestList, 2)
	resourceRequests[0] = ResourceRequest{url: "testURL", dest: "testDest", contentLength: 1000, isAcceptRange: true, status: AVAILABLE}
	resourceRequests[1] = ResourceRequest{url: "testURL2", dest: "testDest2", contentLength: 1000, isAcceptRange: false, status: AVAILABLE}
	return resourceRequests
}

func TestDownloadResources(t *testing.T) {
	testResourceRequest := DownloaderClusterMock{}.FetchResourceRequests()
	testChuckSize := uint64(100)

	testResource := testResourceRequest.ToResources(testChuckSize)
	if len(testResource) != 2 {
		t.Errorf("Expected %d, got %d", 2, len(testResource))
	}

	if len(testResource[0]._segments) != 10 {
		t.Errorf("Expected %d, got %d", 10, len(testResource[0]._segments))
	}

	if len(testResource[1]._segments) != 1 {
		t.Errorf("Expected %d, got %d", 1, len(testResource[1]._segments))
	}
}

func TestConsumeJobs(t *testing.T) {
	type StubWorker struct {
		flag bool
	}

	workers := make([]*StubWorker, 1000)
	for i := range workers {
		workers[i] = &StubWorker{flag: false}
	}

	jobs := make([]func(worker *StubWorker), 1000)
	for i := range jobs {
		jobs[i] = func(w *StubWorker) {
			w.flag = true
		}
	}

	ConsumeJobs(workers, jobs)

	for _, worker := range workers {
		if !worker.flag {
			t.Errorf("Expected %v, got %v", true, worker.flag)
		}
	}
}
