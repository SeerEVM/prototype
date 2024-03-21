package dependencyGraph

import (
	"prophetEVM/core/state"
	"prophetEVM/minHeap"
)

type DependencyGraph struct {
	// map[tx_index]tx_index，记录每笔交易的所有依赖交易中序号最大的那个
	TxDependencyMap map[int]int
}

func ConstructDependencyGraph(txNum int, Hready *minHeap.ReadyHeap, Hcommit *minHeap.CommitHeap, stateDb *state.IcseStateDB) *DependencyGraph {
	dp := &DependencyGraph{
		TxDependencyMap: make(map[int]int),
	}
	// 将所有交易转化成任务塞进堆Hready
	for i := 0; i < txNum; i++ {
		Hready.Push(i, 0, -1, true)
	}
	// 等待所有交易执行完成
	next := 0
	for true {
		txAfterExecution := Hcommit.Pop()
		if txAfterExecution == nil {
			continue
		}
		if txAfterExecution.Index != next {
			Hcommit.Push(txAfterExecution)
		} else {
			next++
			if next == txNum {
				break
			}
		}
	}

	for i := 0; i < txNum; i++ {
		dp.TxDependencyMap[i] = stateDb.GetDependency(i)
	}
	return dp
}
