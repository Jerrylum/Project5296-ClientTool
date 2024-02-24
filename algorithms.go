package main

import (
	"fmt"
	"sync"
)

func ConsumeJobs[T any](workers []*T, jobs []func(worker *T)) {
	wg := &sync.WaitGroup{}
	consumed := 0

	workerQueue := make(chan *T, len(workers))
	for _, worker := range workers {
		putWorker := worker
		workerQueue <- putWorker
	}

	jobsQueue := make(chan *func(worker *T), len(jobs))
	for _, job := range jobs {
		putJob := job
		jobsQueue <- &putJob
	}

	// assoicate a worker with a job
	for i := 0; i < len(jobs); i++ {
		wg.Add(1)
		go func(worker *T) {
			defer wg.Done()
			(*<-jobsQueue)(worker)
			consumed++
			workerQueue <- worker
			fmt.Println("Consumed", consumed)
		}(<-workerQueue)
	}

	wg.Wait()
}
