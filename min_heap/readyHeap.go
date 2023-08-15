package minHeap

import (
	"container/heap"
	"sync"
)

type ReadyItem struct {
	Index          int
	StorageVersion int
}

type ReadyHeap struct {
	readyHeap *readyHeap
	mutex     sync.Mutex
}

func NewReadyHeap() *ReadyHeap {
	h := &ReadyHeap{
		readyHeap: new(readyHeap),
	}
	heap.Init(h.readyHeap)

	return h
}

func (h *ReadyHeap) Push(Index int, StorageVersion int) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	nodeToPush := &ReadyItem{
		Index:          Index,
		StorageVersion: StorageVersion,
	}
	heap.Push(h.readyHeap, nodeToPush)
}

func (h *ReadyHeap) Pop() *ReadyItem {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.readyHeap.Len() <= 0 {
		return nil
	}
	newNode := heap.Pop(h.readyHeap).(*ReadyItem)
	return newNode
}

type readyHeap []*ReadyItem

func (h readyHeap) Len() int {
	return len(h)
}

func (h readyHeap) Less(i, j int) bool {
	return h[i].Index < h[j].Index || (h[i].Index == h[j].Index && h[i].StorageVersion < h[j].StorageVersion)
}

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *readyHeap) Push(x interface{}) {
	item := x.(*ReadyItem)
	*h = append(*h, item)
}

func (h *readyHeap) Pop() interface{} {
	x := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	return x
}
