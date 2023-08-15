package core

import (
	"fmt"
	"github.com/ethereum/go-ethereum/params"
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
	publicStateDB *state.StateDB
	// 交易专属db，保存交易读写数据
	txStateDB *state.IcseTransaction
	// 整个区块和区块中的交易
	block *types.Block
	// 区块上下文，一次性生成且不能改动
	blockContext vm.BlockContext
	// 链配置，用于生成EVM
	chainConfig *params.ChainConfig
	// 任务池，线程从这里面获取新任务并执行
	taskPool *minHeap.ReadyHeap
}

// NewThread creates a new instance of ICSE thread
func NewThread(stateDB *state.StateDB, block *types.Block, blockContext vm.BlockContext, readyHeap *minHeap.ReadyHeap) *ICSEThread {
	it := &ICSEThread{
		publicStateDB: stateDB,
		block:         block,
		blockContext:  blockContext,
		taskPool:      readyHeap,
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

	it.txStateDB = state.NewIcseTxStateDB(it.publicStateDB)
	vmenv := vm.NewEVM(it.blockContext, vm.TxContext{}, it.txStateDB, config.MainnetChainConfig, vm.Config{EnablePreimageRecording: false})

	msg, err := TransactionToMessage(tx, types.MakeSigner(config.MainnetChainConfig, header.Number), header.BaseFee)
	if err != nil {
		log.Panic(fmt.Errorf("could format transaction %+v to message", task))
	}
	it.txStateDB.SetTxContext(tx.Hash(), task.Index)
	receipt, err := applyTransaction(msg, config.MainnetChainConfig, gp, it.txStateDB, blockNumber, blockHash, tx, usedGas, vmenv)
	if err != nil {
		log.Panic(fmt.Errorf("could apply transaction %+v", task))
	}

	return receipt, receipt.Logs
}
