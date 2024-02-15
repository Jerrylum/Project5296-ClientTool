package main

import "sync"

type ThreadSafeSortedList[T any] struct {
	list  []*T
	less  func(i, j *T) bool
	mutex sync.Mutex
}

func (ls *ThreadSafeSortedList[T]) Add(item *T) {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()

	// Time complexity: O(n)
	for i, listItem := range ls.list {
		if ls.less(item, listItem) {
			ls.list = append(ls.list[:i], append([]*T{item}, ls.list[i:]...)...)
			return
		}
	}

	ls.list = append(ls.list, item)
}

func (ls *ThreadSafeSortedList[T]) Remove(item *T) bool {
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

func (ls *ThreadSafeSortedList[T]) Len() int {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()

	return len(ls.list)
}

func (ls *ThreadSafeSortedList[T]) Pop() *T {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()

	if len(ls.list) == 0 {
		return nil
	}

	item := ls.list[0]
	ls.list = ls.list[1:]
	return item
}
