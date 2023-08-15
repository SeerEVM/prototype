package minHeap

import (
	"container/heap"
	"sync"
)

type TxsItem struct {
	StorageVersion int
	Index          int
}

type TxsHeap struct {
	txsHeap *txsHeap
	mutex   sync.Mutex
}

func NewTxsHeap() *TxsHeap {
	h := &TxsHeap{
		txsHeap: new(txsHeap),
	}
	heap.Init(h.txsHeap)

	return h
}

func (h *TxsHeap) Push(StorageVersion int, Index int) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	nodeToPush := &TxsItem{
		StorageVersion: StorageVersion,
		Index:          Index,
	}
	heap.Push(h.txsHeap, nodeToPush)
}

func (h *TxsHeap) Pop() *TxsItem {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if h.txsHeap.Len() <= 0 {
		return nil
	}
	newNode := heap.Pop(h.txsHeap).(*TxsItem)
	return newNode
}

type txsHeap []*TxsItem

func (h txsHeap) Len() int {
	return len(h)
}

func (h txsHeap) Less(i, j int) bool {
	return h[i].StorageVersion < h[j].StorageVersion || (h[i].StorageVersion == h[j].StorageVersion && h[i].Index < h[j].Index)
}

func (h txsHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *txsHeap) Push(x interface{}) {
	item := x.(*TxsItem)
	*h = append(*h, item)
}

func (h *txsHeap) Pop() interface{} {
	x := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	return x
}
