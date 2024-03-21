package core

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/params"
	"log"
	"math/big"
	"prophetEVM/core/state"
	"prophetEVM/core/types"
	"prophetEVM/core/vm"
	"prophetEVM/minHeap"
	"strings"
)

// ICSEThread implements the STM thread for executing and validating txs
type ICSEThread struct {
	// thread id
	ThreadID int
	// 多线程共用的公共存储db，保存多版本数据
	publicStateDB *state.IcseStateDB
	// publicStateDB的克隆，专门用于模拟执行交易生成读写集，进而生成交易依赖图，在正式执行时不使用该db
	simulationStateDB *state.IcseStateDB
	// publicStateDB的克隆，专门用于生成交易依赖图，在正式执行时不使用该db
	constructionStateDB *state.IcseStateDB
	// 交易专属db，保存交易读写数据
	txStateDB *state.IcseTransaction
	// 待执行交易池，线程从这里面获取新任务并执行
	taskPool *minHeap.ReadyHeap
	// 待提交交易池，线程执行完任务后将交易放入
	commitPool *minHeap.CommitHeap

	// 存储变量相关的分支信息表
	varTable *vm.VarTable
	// 多版本缓存
	mvCache *state.MVCache
	// 预执行表
	preExecutionTable *vm.PreExecutionTable

	// 整个区块和区块中的交易
	block *types.Block
	// 区块上下文，一次性生成且不能改动
	blockContext vm.BlockContext
	// chainConfig
	chainConfig *params.ChainConfig
}

// NewThread creates a new instance of ICSE thread
func NewThread(threadId int, stateDB *state.IcseStateDB, stateDbForConstruction *state.IcseStateDB, readyHeap *minHeap.ReadyHeap,
	commitHeap *minHeap.CommitHeap, varTable *vm.VarTable, mvCache *state.MVCache, preExecutionTable *vm.PreExecutionTable,
	block *types.Block, blockContext vm.BlockContext, chainConfig *params.ChainConfig) *ICSEThread {
	it := &ICSEThread{
		ThreadID:            threadId,
		publicStateDB:       stateDB,
		simulationStateDB:   stateDB.Copy(),
		constructionStateDB: stateDbForConstruction,
		txStateDB:           nil,
		taskPool:            readyHeap,
		commitPool:          commitHeap,
		varTable:            varTable,
		mvCache:             mvCache,
		preExecutionTable:   preExecutionTable,
		block:               block,
		blockContext:        blockContext,
		chainConfig:         chainConfig,
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
		readSet, writeSet, _, _ := it.executeTask(task, PrintDetails)
		commitItem := minHeap.NewCommitItem(task.Index, task.Incarnation, task.StorageVersion, readSet, writeSet)
		it.commitPool.Push(commitItem)
		if PrintDetails {
			fmt.Printf("线程%d完成任务%+v\n", it.ThreadID, task)
		}
	}
}

// SingleRun runs a single thread for testing perfect dependency
func (it *ICSEThread) SingleRun(txNum int, PrintDetails bool) {
	counter := 0
	for true {
		task := it.taskPool.Pop()
		if task == nil && counter == txNum {
			break
		} else if task == nil && counter == 0 {
			continue
		}
		if PrintDetails {
			fmt.Printf("线程%d开始任务%+v\n", it.ThreadID, task)
		}
		readSet, writeSet, _, _ := it.executeTask(task, PrintDetails)
		commitItem := minHeap.NewCommitItem(task.Index, task.Incarnation, task.StorageVersion, readSet, writeSet)
		it.commitPool.Push(commitItem)
		counter++
		if PrintDetails {
			fmt.Printf("线程%d完成任务%+v\n", it.ThreadID, task)
		}
	}
}

// PreExecution executes transactions speculatively with a branch prediction approach
func (it *ICSEThread) PreExecution(txSet map[common.Hash]*types.Transaction) {
	var (
		header = it.block.Header()
		gp     = new(GasPool).AddGas(it.block.GasLimit())
	)

	// TODO: 可以随时接收并处理gas费更高的事务
	for i, tx := range it.block.Transactions() {
		// 合约创建交易不需要预执行
		if tx.To() == nil {
			continue
		}
		res, _ := it.preExecutionTable.GetResult(tx.Hash())
		if res != nil {
			// TODO: 首先检查预执行表中有没有相关的交易信息（之前被预执行但是没有上链），可以利用缓存结果快速预执行
		} else {
			res = it.preExecutionTable.InitialResult(tx.Hash())
		}

		msg, err1 := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
		if err1 != nil {
			log.Panic(fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err1))
		}
		tip := math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), it.block.BaseFee()))
		txContext := NewEVMTxContext(msg, tx.Hash(), tip)
		res.UpdateTxContext(txContext)
		vmenv := vm.NewEVM2(it.blockContext, txContext, it.txStateDB, it.varTable, it.preExecutionTable,
			it.mvCache, it.chainConfig, vm.Config{EnablePreimageRecording: false}, true)
		err2 := applyProphetTransaction(msg, gp, vmenv, txSet, it.block.BaseFee())
		if err2 != nil {
			log.Panic(fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err2))
		}
	}
	// 清理多版本缓存
	it.mvCache.ClearCache()
}

// SingleFastExecution executes transactions in a fast-path mode using a single thread
func (it *ICSEThread) SingleFastExecution(block *types.Block, stateDB *state.IcseTransaction) (*common.Hash, types.Receipts, []*types.Log, uint64, error) {
	var (
		receipts    types.Receipts
		usedGas     = new(uint64)
		header      = block.Header()
		blockHash   = block.Hash()
		blockNumber = block.Number()
		allLogs     []*types.Log
		gp          = new(GasPool).AddGas(block.GasLimit())
	)

	// Iterate over and process the individual transactions
	for i, tx := range block.Transactions() {
		ret, _ := it.preExecutionTable.GetResult(tx.Hash())
		txContext := ret.GetTxContext()
		msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		vmenv := vm.NewEVM2(it.blockContext, txContext, stateDB, it.varTable, it.preExecutionTable, it.mvCache,
			it.chainConfig, vm.Config{EnablePreimageRecording: false}, false)
		receipt, err := applyFastPath(msg, tx, vmenv, ret, gp, stateDB, blockNumber, blockHash, usedGas)
		if err != nil {
			return nil, nil, nil, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		stateDB.Validation(true)
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
		// 设置上链标识
		it.preExecutionTable.SetOnChain(tx.Hash())
	}
	// 清理分支变量表与预执行表的冗余项
	it.varTable.Sweep()
	it.preExecutionTable.ClearTable()

	// Fail if Shanghai not enabled and len(withdrawals) is non-zero.
	withdrawals := block.Withdrawals()
	if len(withdrawals) > 0 && !it.chainConfig.IsShanghai(block.Time()) {
		return nil, nil, nil, 0, fmt.Errorf("withdrawals before shanghai")
	}
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	//accumulateRewards(it.chainConfig, stateDB, header, block.Uncles())
	//root := stateDB.IntermediateRoot(p.config.IsEIP158(header.Number))
	return nil, receipts, allLogs, *usedGas, nil
}

// executeTask executes a transaction without a fast path
func (it *ICSEThread) executeTask(task *minHeap.ReadyItem, PrintDetails bool) ([]*state.ReadLoc, state.WriteSets, *types.Receipt, []*types.Log) {
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
		publicStateDB = it.simulationStateDB
		//publicStateDB = it.constructionStateDB
	} else {
		publicStateDB = it.publicStateDB
	}

	// new tx statedb
	it.txStateDB = state.NewIcseTxStateDB(tx, task.Index, task.Incarnation, task.StorageVersion, publicStateDB)

	// create evm
	vmenv := vm.NewEVM(it.blockContext, vm.TxContext{}, it.txStateDB, it.chainConfig, vm.Config{EnablePreimageRecording: false}, false)
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

	receipt, err := applyIcseTransaction(msg, gp, it.txStateDB, blockNumber, blockHash, tx, usedGas, vmenv)
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

	// 读写集记录到public_statedb中 (真正执行时需要等到commit后才能刷新进statedb)
	if task.IsSimulation {
		it.constructionStateDB.Record(&state.TxInfoMini{Index: task.Index, Incarnation: task.Incarnation}, readSet, writeSet)
	}
	//publicStateDB.Record(&state.TxInfoMini{Index: task.Index, Incarnation: task.Incarnation}, readSet, writeSet)

	return readSet, writeSet, receipt, receipt.Logs
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
