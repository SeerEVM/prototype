package core

import (
	"fmt"
	"icse/config"
	"icse/core/state"
	"icse/core/types"
	"icse/core/vm"
	minHeap "icse/min_heap"
	"log"
	"time"
)

// ICSEThread implements the STM thread for executing and validating txs
type ICSEThread struct {
	// 多线程共用的公共存储db，保存多版本数据
	publicStateDB *state.IcseStateDB
	// 交易专属db，保存交易读写数据
	txStateDB *state.IcseTransaction
	// 整个区块和区块中的交易
	block *types.Block
	// 区块上下文，一次性生成且不能改动
	blockContext vm.BlockContext
	// 待执行交易池，线程从这里面获取新任务并执行
	taskPool *minHeap.ReadyHeap
	// 待提交交易池，线程执行完任务后将交易放入
	commitPool *minHeap.CommitHeap
}

// NewThread creates a new instance of ICSE thread
func NewThread(stateDB *state.IcseStateDB, block *types.Block, blockContext vm.BlockContext, readyHeap *minHeap.ReadyHeap, commitHeap *minHeap.CommitHeap) *ICSEThread {
	it := &ICSEThread{
		publicStateDB: stateDB,
		txStateDB:     nil,
		block:         block,
		blockContext:  blockContext,
		taskPool:      readyHeap,
		commitPool:    commitHeap,
	}
	return it
}

func (it *ICSEThread) Run() {
	for true {
		task := it.taskPool.Pop()
		if task == nil {
			time.Sleep(100 * time.Millisecond)
			break
		}
		it.executeTask(task)
		it.commitPool.Push(task.Index, task.Incarnation, task.StorageVersion)
	}
}

func (it *ICSEThread) executeTask(task *minHeap.ReadyItem) (*types.Receipt, []*types.Log) {
	var (
		usedGas     = new(uint64)
		header      = it.block.Header()
		blockHash   = it.block.Hash()
		blockNumber = it.block.Number()
		gp          = new(GasPool).AddGas(it.block.GasLimit())
		tx          = it.block.Transactions()[task.Index]
	)

	// new tx statedb
	it.txStateDB = state.NewIcseTxStateDB(tx, task.Index, task.Incarnation, task.StorageVersion, it.publicStateDB)

	// create evm and execute transaction
	vmenv := vm.NewEVM(it.blockContext, vm.TxContext{}, it.txStateDB, config.MainnetChainConfig, vm.Config{EnablePreimageRecording: false})
	msg, err := TransactionToMessage(tx, types.MakeSigner(config.MainnetChainConfig, header.Number), header.BaseFee)
	if err != nil {
		log.Panic(fmt.Errorf("could format transaction %+v to message", task))
	}
	it.txStateDB.SetTxContext(tx.Hash(), task.Index)
	receipt, err := applyIcseTransaction(msg, config.MainnetChainConfig, gp, it.publicStateDB, it.txStateDB, blockNumber, blockHash, tx, usedGas, vmenv)
	if err != nil {
		log.Panic(fmt.Errorf("could apply transaction %+v", task))
	}

	// get read/write set and store into public statedb
	readSet, writeSet := it.txStateDB.OutputRWSet()
	it.publicStateDB.Record(&state.TxInfoMini{Index: task.Index, Incarnation: task.Incarnation}, readSet, writeSet)

	return receipt, receipt.Logs
}
