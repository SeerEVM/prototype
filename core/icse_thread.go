package core

import (
	"fmt"
	"github.com/ethereum/go-ethereum/params"
	"icse/core/state"
	"icse/core/types"
	"icse/core/vm"
	"icse/minHeap"
	"log"
	"math/big"
	"strings"
)

// ICSEThread implements the STM thread for executing and validating txs
type ICSEThread struct {
	// thread id
	ThreadID int
	// 多线程共用的公共存储db，保存多版本数据
	publicStateDB *state.IcseStateDB
	// publicStateDB的克隆，专门用于模拟执行交易生成读写集，进而生成交易依赖图，在正式执行时不使用该db
	publicStateDB2 *state.IcseStateDB
	// 交易专属db，保存交易读写数据
	txStateDB *state.IcseTransaction
	// 待执行交易池，线程从这里面获取新任务并执行
	taskPool *minHeap.ReadyHeap
	// 待提交交易池，线程执行完任务后将交易放入
	commitPool *minHeap.CommitHeap

	// 整个区块和区块中的交易
	block *types.Block
	// 区块上下文，一次性生成且不能改动
	blockContext vm.BlockContext
	// chainConfig
	chainConfig *params.ChainConfig
}

// NewThread creates a new instance of ICSE thread
func NewThread(threadId int, stateDB *state.IcseStateDB, stateDbForSimulation *state.IcseStateDB, readyHeap *minHeap.ReadyHeap, commitHeap *minHeap.CommitHeap, block *types.Block, blockContext vm.BlockContext, chainConfig *params.ChainConfig) *ICSEThread {
	it := &ICSEThread{
		ThreadID:       threadId,
		publicStateDB:  stateDB,
		publicStateDB2: stateDbForSimulation,
		txStateDB:      nil,
		taskPool:       readyHeap,
		commitPool:     commitHeap,
		block:          block,
		blockContext:   blockContext,
		chainConfig:    chainConfig,
	}
	return it
}

func (it *ICSEThread) Run(PrintDetails bool) {
	for true {
		task := it.taskPool.Pop()
		if task == nil {
			continue
		}
		if PrintDetails {
			fmt.Printf("线程%d开始任务%+v\n", it.ThreadID, task)
		}
		it.executeTask(task, PrintDetails)
		it.commitPool.Push(task.Index, task.Incarnation, task.StorageVersion)
		if PrintDetails {
			fmt.Printf("线程%d完成任务%+v\n", it.ThreadID, task)
		}
	}
}

func (it *ICSEThread) executeTask(task *minHeap.ReadyItem, PrintDetails bool) (*types.Receipt, []*types.Log) {
	var (
		usedGas     = new(uint64)
		header      = it.block.Header()
		blockHash   = it.block.Hash()
		blockNumber = it.block.Number()
		gp          = new(GasPool).AddGas(it.block.GasLimit())
		tx          = it.block.Transactions()[task.Index]
	)

	// 如果目前正在生成依赖图阶段，则用publicStateDB2；如果是正式执行，则用publicStateDB
	var publicStateDB *state.IcseStateDB
	if task.IsSimulation {
		publicStateDB = it.publicStateDB2
	} else {
		publicStateDB = it.publicStateDB
	}

	// new tx statedb
	it.txStateDB = state.NewIcseTxStateDB(tx, task.Index, task.Incarnation, task.StorageVersion, publicStateDB)

	// create evm
	vmenv := vm.NewEVM(it.blockContext, vm.TxContext{}, it.txStateDB, it.chainConfig, vm.Config{EnablePreimageRecording: false})
	msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
	if err != nil {
		log.Panic(fmt.Errorf("could not format transaction %+v to message", task))
	}
	it.txStateDB.SetTxContext(tx.Hash(), task.Index)

	// 避免Nonce错误
	it.txStateDB.SetNonce(msg.From, msg.Nonce)
	// 避免Balance错误
	mgval := new(big.Int).SetUint64(msg.GasLimit)
	mgval = mgval.Mul(mgval, msg.GasPrice)
	balanceCheck := mgval
	if msg.GasFeeCap != nil {
		balanceCheck = new(big.Int).SetUint64(msg.GasLimit)
		balanceCheck = balanceCheck.Mul(balanceCheck, msg.GasFeeCap)
		balanceCheck.Add(balanceCheck, msg.Value)
	}
	it.txStateDB.AddBalance(msg.From, balanceCheck)

	receipt, err := applyIcseTransaction(msg, it.chainConfig, gp, publicStateDB, it.txStateDB, blockNumber, blockHash, tx, usedGas, vmenv)
	if err != nil {
		log.Panic(fmt.Errorf("could not apply transaction %+v: %s", task, err))
	}

	// get read/write set and store into public statedb
	readSet, writeSet := it.txStateDB.OutputRWSet() // 得到tx_statedb中记录的读写集

	// 过滤读写集中的矿工地址
	readSet, writeSet = FilterRWSet(readSet, writeSet, it.block.Header().Coinbase.String())

	// 打印读写集
	if PrintDetails {
		var rs []string
		for _, r := range readSet {
			rs = append(rs, r.Location.String())
		}
		fmt.Printf("交易%+v的读集是：\n%s\n交易%+v的写集是：\n%s\n", task, strings.Join(rs, "\n"), task, writeSet)
	}

	// 读写集记录到public_statedb中
	publicStateDB.Record(&state.TxInfoMini{Index: task.Index, Incarnation: task.Incarnation}, readSet, writeSet)

	return receipt, receipt.Logs
}

// FilterRWSet 将读写集中包含的矿工地址删除（因为每个交易都会读写矿工地址）
func FilterRWSet(readSet []*state.ReadLoc, writeSet state.WriteSets, minerAddr string) ([]*state.ReadLoc, state.WriteSets) {
	filteredReadSet := make([]*state.ReadLoc, 0, len(readSet))
	for _, r := range readSet {
		addr, _ := r.Location.Address()
		if addr.String() != minerAddr {
			filteredReadSet = append(filteredReadSet, r)
		}
	}

	for k, _ := range writeSet {
		if k.String() == minerAddr {
			delete(writeSet, k)
		}
	}

	return filteredReadSet, writeSet
}
