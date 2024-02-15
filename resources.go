package main

import (
	"fmt"
	"os"
)

type ResourceStatus int

const (
	PENDING ResourceStatus = iota
	DOWNLOADING
	DOWNLOADED
	DOWNLOAD_FAILED
)

type Resource struct {
	url              string
	dest             string
	contentLength    uint64 // in bytes
	isAcceptRange    bool
	_fd              *os.File
	_segments        []*ResourceSegment
	_writtenSegments []*ResourceSegment
}

func (r *Resource) SliceSegments(chunkSize uint64) []*ResourceSegment {
	if r.isAcceptRange {
		segments := []*ResourceSegment{}
		for idx := uint64(0); idx < r.contentLength; {
			maxChunkSize := min(r.contentLength, idx+chunkSize)
			segment := ResourceSegment{resource: r, from: idx, to: maxChunkSize, ttl: 3, status: PENDING}
			segments = append(segments, &segment)
			idx += maxChunkSize
		}
		r._segments = segments
	} else {
		segment := ResourceSegment{resource: r, from: 0, to: r.contentLength, ttl: 3, status: PENDING}
		r._segments = []*ResourceSegment{&segment}
	}
	return r._segments
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
		if seg.status != PENDING {
			isAllPending = false
		}
		if seg.status == DOWNLOADING {
			return DOWNLOADING
		}
		if seg.status != DOWNLOADED {
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
		return 0, fmt.Errorf("the file is not opened")
	}
	return r._fd.WriteAt(b, off)
}

type ResourceSegment struct {
	resource *Resource
	from     uint64 // inclusive
	to       uint64 // exclusive
	ttl      uint8
	status   ResourceStatus
}

func (rs *ResourceSegment) ContentLength() uint64 {
	return rs.to - rs.from
}

func (rs *ResourceSegment) StartDownload() {
	if rs.status != PENDING {
		panic("The segment is not pending")
	}
	if rs.ttl == 0 {
		panic("The segment has no more ttl")
	}
	rs.status = DOWNLOADING

	if err := rs.resource.OpenFile(); err != nil {
		panic(err)
	}
}

func (rs *ResourceSegment) CancelDownload() {
	if rs.status != DOWNLOADING {
		panic("The segment is not downloading")
	}
	if rs.ttl == 0 {
		panic("The segment has no more ttl")
	}
	rs.ttl--
	if rs.ttl == 0 {
		rs.status = DOWNLOAD_FAILED
	} else {
		rs.status = PENDING
	}
}

func (rs *ResourceSegment) FinishDownload() {
	if rs.status != DOWNLOADING {
		panic("The segment is not downloading")
	}
	rs.status = DOWNLOADED

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
	if rs.status != DOWNLOADING {
		return 0, fmt.Errorf("the segment is not downloading")
	}
	return rs.resource.WriteAt(b, off)
}

func (firstHalf *ResourceSegment) Split() *ResourceSegment {
	r := firstHalf.resource
	middle := firstHalf.from + (firstHalf.to-firstHalf.from)/2
	end := firstHalf.to
	secondHalf := ResourceSegment{resource: r, from: middle, to: end, ttl: 3, status: PENDING}
	firstHalf.to = middle
	r._segments = append(r._segments, &secondHalf)
	return &secondHalf
}
