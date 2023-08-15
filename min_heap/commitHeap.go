package minHeap

import (
	"container/heap"
	"sync"
)

type CommitItem struct {
	Index          int
	StorageVersion int
}

type CommitHeap struct {
	commitHeap *commitHeap
	mutex      sync.Mutex
}

func NewCommitHeap() *CommitHeap {
	h := &CommitHeap{
		commitHeap: new(commitHeap),
	}
	heap.Init(h.commitHeap)

	return h
}

func (h *CommitHeap) Push(Index int, StorageVersion int) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	nodeToPush := &CommitItem{
		Index:          Index,
		StorageVersion: StorageVersion,
	}
	heap.Push(h.commitHeap, nodeToPush)
}

func (h *CommitHeap) Pop() *CommitItem {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.commitHeap.Len() <= 0 {
		return nil
	}
	newNode := heap.Pop(h.commitHeap).(*CommitItem)
	return newNode
}

type commitHeap []*CommitItem

func (h commitHeap) Len() int {
	return len(h)
}

func (h commitHeap) Less(i, j int) bool {
	return h[i].Index < h[j].Index || (h[i].Index == h[j].Index && h[i].StorageVersion < h[j].StorageVersion)
}

func (h commitHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *commitHeap) Push(x interface{}) {
	item := x.(*CommitItem)
	*h = append(*h, item)
}

func (h *commitHeap) Pop() interface{} {
	x := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	return x
}
