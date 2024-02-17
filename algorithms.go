package main

func ConsumeJobs[T any](workers []*T, jobs []func(worker *T)) {
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
		go func(worker *T) {
			(*<-jobsQueue)(worker)
			consumed++
			workerQueue <- worker
		}(<-workerQueue)
	}

	// wait for all jobs to be consumed
	for consumed < len(jobs) {
		<-workerQueue
	}
}
