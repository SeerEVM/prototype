package core

import (
	"context"
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
	"sort"
	"strings"
	"time"
)

// ICSEThread implements the STM thread for executing and validating txs
type ICSEThread struct {
	// thread id
	ThreadID int
	// 以太坊原生db
	commonStateDB *state.StateDB
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

	// 记录分支预测命中率
	totalPredictions       int
	unsatisfiedPredictions int
	satisfiedTxs           int

	// 接收交易的通道
	txSource   <-chan []*types.Transaction
	disorder   <-chan *types.Transaction
	txSet      <-chan map[common.Hash]*types.Transaction
	tip        <-chan map[common.Hash]*big.Int
	blkChannel <-chan *types.Block
}

// NewThread creates a new instance of ICSE thread
func NewThread(threadId int, stateDB *state.StateDB, publicStateDB *state.IcseStateDB, stateDbForConstruction *state.IcseStateDB, readyHeap *minHeap.ReadyHeap,
	commitHeap *minHeap.CommitHeap, varTable *vm.VarTable, mvCache *state.MVCache, preExecutionTable *vm.PreExecutionTable,
	block *types.Block, blockContext vm.BlockContext, chainConfig *params.ChainConfig) *ICSEThread {
	it := &ICSEThread{
		ThreadID:               threadId,
		commonStateDB:          stateDB,
		publicStateDB:          publicStateDB,
		simulationStateDB:      nil,
		constructionStateDB:    stateDbForConstruction,
		txStateDB:              nil,
		taskPool:               readyHeap,
		commitPool:             commitHeap,
		varTable:               varTable,
		mvCache:                mvCache,
		preExecutionTable:      preExecutionTable,
		block:                  block,
		blockContext:           blockContext,
		chainConfig:            chainConfig,
		totalPredictions:       0,
		unsatisfiedPredictions: 0,
		satisfiedTxs:           0,
	}
	return it
}

func (it *ICSEThread) Run(ctx context.Context, isFastPath, PrintDetails bool) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			task := it.taskPool.Pop()
			if task == nil {
				continue
			}
			if PrintDetails {
				fmt.Printf("线程%d开始任务%+v\n", it.ThreadID, task)
			}
			var (
				readSet  []*state.ReadLoc
				writeSet state.WriteSets
			)
			if isFastPath {
				readSet, writeSet, _, _ = it.executeTaskFastPath(task, PrintDetails)
			} else {
				readSet, writeSet, _, _ = it.executeTask(task, PrintDetails)
			}
			commitItem := minHeap.NewCommitItem(task.Index, task.Incarnation, task.StorageVersion, readSet, writeSet)
			it.commitPool.Push(commitItem)
			if PrintDetails {
				fmt.Printf("线程%d完成任务%+v\n", it.ThreadID, task)
			}
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
func (it *ICSEThread) PreExecution(txSet map[common.Hash]*types.Transaction, tipMap map[common.Hash]*big.Int, isNative, enablePerceptron, storeCheckpoint bool) (map[common.Hash]vm.ReadSet, map[common.Hash]vm.WriteSet) {
	var (
		header = it.block.Header()
		gp     = new(GasPool).AddGas(it.block.GasLimit())
	)

	for _, tx := range it.block.Transactions() {
		it.singlePreEexcution(tx, header, txSet, tipMap, gp, isNative, false, enablePerceptron, storeCheckpoint)
	}
	// 清理多版本缓存
	it.mvCache.ClearCache()
	return it.OutputRWSets()
}

// PreExecutionWithDisorder executes transactions speculatively with a branch prediction approach (under the case of disordered tx input)
func (it *ICSEThread) PreExecutionWithDisorder(isNative, enableRepair, enablePerceptron bool) time.Duration {
	block := <-it.blkChannel
	txMap := <-it.txSet
	tipMap := <-it.tip
	executionQueue := <-it.txSource

	var (
		header       = block.Header()
		gp           = new(GasPool).AddGas(block.GasLimit())
		processedTxs = make([]*types.Transaction, 0, len(txMap))
	)

	t1 := time.Now()
	for {
		if len(processedTxs) == len(txMap) {
			break
		}
		select {
		case insertTx := <-it.disorder:
			if !enableRepair {
				// 立刻执行
				it.singlePreEexcution(insertTx, header, txMap, tipMap, gp, isNative, enableRepair, enablePerceptron, true)
				processedTxs = append(processedTxs, insertTx)
				continue
			}
			insertTip := math.BigMin(insertTx.GasTipCap(), new(big.Int).Sub(insertTx.GasFeeCap(), block.BaseFee()))
			if len(processedTxs) > 0 {
				latest := processedTxs[len(processedTxs)-1]
				latestTip := math.BigMin(latest.GasTipCap(), new(big.Int).Sub(latest.GasFeeCap(), block.BaseFee()))
				if insertTip.Cmp(latestTip) <= 0 {
					// insert into the executionQueue
					insertLoc := sort.Search(len(executionQueue), func(i int) bool {
						tip := math.BigMin(executionQueue[i].GasTipCap(), new(big.Int).Sub(executionQueue[i].GasFeeCap(), block.BaseFee()))
						if insertTip.Cmp(tip) > 0 {
							return true
						} else {
							return false
						}
					})
					executionQueue = append(executionQueue[:insertLoc], append([]*types.Transaction{insertTx}, executionQueue[insertLoc:]...)...)
					continue
				}
			}
			// 立刻执行
			it.singlePreEexcution(insertTx, header, txMap, tipMap, gp, isNative, enableRepair, enablePerceptron, true)
			processedTxs = append(processedTxs, insertTx)
		default:
			// 立刻执行
			if len(executionQueue) == 0 {
				continue
			}
			nextTx := executionQueue[0]
			it.singlePreEexcution(nextTx, header, txMap, tipMap, gp, isNative, enableRepair, enablePerceptron, true)
			executionQueue = executionQueue[1:]
			processedTxs = append(processedTxs, nextTx)
		}
	}
	e1 := time.Since(t1)
	fmt.Printf("Pre-execution latency is: %s\n", e1)

	// 清理多版本缓存
	it.mvCache.ClearCache()
	return e1
}

func (it *ICSEThread) singlePreEexcution(tx *types.Transaction, header *types.Header, txMap map[common.Hash]*types.Transaction, tipMap map[common.Hash]*big.Int, gp *GasPool, isNative, enableRepair, enablePerceptron, storeCheckpoint bool) {
	var stateDB vm.StateDB
	if isNative {
		stateDB = it.commonStateDB.Copy()
	} else {
		// new tx statedb
		stateDB = state.NewIcseTxStateDB(tx, 0, 0, -1, it.publicStateDB)
	}

	msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
	if err != nil {
		fmt.Printf("could not apply tx [%v]: %v\n", tx.Hash().Hex(), err)
	}
	tip := tipMap[tx.Hash()]
	txContext := NewEVMTxContext(msg, tx.Hash(), tip)
	vmenv := vm.NewEVM2(it.blockContext, txContext, stateDB, it.varTable, it.preExecutionTable,
		it.mvCache, it.chainConfig, vm.Config{EnablePreimageRecording: false}, true, enablePerceptron, storeCheckpoint)

	res := it.preExecutionTable.InitialResult(tx.Hash())
	res.UpdateTxContext(txContext)

	// 避免Nonce错误
	stateDB.SetNonce(msg.From, msg.Nonce)
	//it.commonStateDB.SetNonce(msg.From, msg.Nonce)
	// 避免Balance错误
	mgval := new(big.Int).SetUint64(msg.GasLimit)
	mgval = mgval.Mul(mgval, msg.GasPrice)
	balanceCheck := mgval
	if msg.GasFeeCap != nil {
		balanceCheck = new(big.Int).SetUint64(msg.GasLimit)
		balanceCheck = balanceCheck.Mul(balanceCheck, msg.GasFeeCap)
		balanceCheck.Add(balanceCheck, msg.Value)
	}
	//it.commonStateDB.AddBalance(msg.From, balanceCheck)
	stateDB.AddBalance(msg.From, balanceCheck)

	_ = applyProphetTransaction(msg, gp, vmenv, txMap, tipMap, enableRepair, enablePerceptron, storeCheckpoint)
	//if err3 != nil {
	//	fmt.Printf("could not apply tx [%v]: %v\n", tx.Hash().Hex(), err3)
	//}
}

// FastExecution executes transactions in a fast-path mode using a single thread
func (it *ICSEThread) FastExecution(stateDB *state.StateDB, recorder *Recorder, enablePerceptron, enableFast, isStatistics bool, fileName string) (*common.Hash, types.Receipts, []*types.Log, uint64, int, error) {
	var (
		receipts    types.Receipts
		usedGas     = new(uint64)
		header      = it.block.Header()
		blockHash   = it.block.Hash()
		blockNumber = it.block.Number()
		allLogs     []*types.Log
		gp          = new(GasPool).AddGas(1000 * it.block.GasLimit())
		ctxNum      int
	)

	t := time.Now()
	// Iterate over and process the individual transactions
	for i, tx := range it.block.Transactions() {
		ret, _ := it.preExecutionTable.GetResult(tx.Hash())
		txContext := ret.GetTxContext()
		msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
		if err != nil {
			return nil, nil, nil, 0, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		// 避免Nonce错误
		stateDB.SetNonce(msg.From, msg.Nonce)
		// 避免Balance错误
		mgval := new(big.Int).SetUint64(msg.GasLimit)
		mgval = mgval.Mul(mgval, msg.GasPrice)
		balanceCheck := mgval
		if msg.GasFeeCap != nil {
			balanceCheck = new(big.Int).SetUint64(msg.GasLimit)
			balanceCheck = balanceCheck.Mul(balanceCheck, msg.GasFeeCap)
			balanceCheck.Add(balanceCheck, msg.Value)
		}
		stateDB.AddBalance(msg.From, balanceCheck)

		vmenv := vm.NewEVM2(it.blockContext, txContext, stateDB, it.varTable, it.preExecutionTable, it.mvCache,
			it.chainConfig, vm.Config{EnablePreimageRecording: false}, false, false, false)
		receipt, isContractCall, err := applyFastPath(msg, tx, vmenv, ret, gp, blockNumber, blockHash, usedGas, it, recorder, enableFast)
		if err != nil {
			return nil, nil, nil, 0, 0, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		if isContractCall {
			ctxNum++
		}
		receipts = append(receipts, receipt)
		allLogs = append(allLogs, receipt.Logs...)
		// 设置上链标识
		it.preExecutionTable.SetOnChain(tx.Hash())
	}
	e := time.Since(t)
	fmt.Printf("Execution latency is: %s\n", e)

	// 更新分支历史与感知器
	if enablePerceptron {
		err := it.UpdateBranchHistory()
		if err != nil {
			return nil, nil, nil, 0, 0, err
		}
	}
	// 清理分支变量表与预执行表的冗余项
	it.varTable.Sweep()
	it.preExecutionTable.ClearTable()
	// 计算分支统计信息
	if isStatistics {
		it.varTable.Statistics(ctxNum, fileName)
	}

	// Fail if Shanghai not enabled and len(withdrawals) is non-zero.
	withdrawals := it.block.Withdrawals()
	if len(withdrawals) > 0 && !it.chainConfig.IsShanghai(it.block.Time()) {
		return nil, nil, nil, 0, 0, fmt.Errorf("withdrawals before shanghai")
	}
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	accumulateRewards(it.chainConfig, stateDB, header, it.block.Uncles())
	root := stateDB.IntermediateRoot(it.chainConfig.IsEIP158(header.Number))
	return &root, receipts, allLogs, *usedGas, ctxNum, nil
}

// FastExecutionRemovingIO executes transactions in a fast-path mode withou modifying states for removing I/O
func (it *ICSEThread) FastExecutionRemovingIO(stateDB *state.StateDB) error {
	var (
		usedGas     = new(uint64)
		header      = it.block.Header()
		blockHash   = it.block.Hash()
		blockNumber = it.block.Number()
		gp          = new(GasPool).AddGas(it.block.GasLimit())
	)

	// Iterate over and process the individual transactions
	for i, tx := range it.block.Transactions() {
		ret, _ := it.preExecutionTable.GetResult(tx.Hash())
		txContext := ret.GetTxContext()
		msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
		if err != nil {
			return fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}

		// 避免Nonce错误
		stateDB.SetNonce(msg.From, msg.Nonce)
		// 避免Balance错误
		mgval := new(big.Int).SetUint64(msg.GasLimit)
		mgval = mgval.Mul(mgval, msg.GasPrice)
		balanceCheck := mgval
		if msg.GasFeeCap != nil {
			balanceCheck = new(big.Int).SetUint64(msg.GasLimit)
			balanceCheck = balanceCheck.Mul(balanceCheck, msg.GasFeeCap)
			balanceCheck.Add(balanceCheck, msg.Value)
		}
		stateDB.AddBalance(msg.From, balanceCheck)

		vmenv := vm.NewEVM2(it.blockContext, txContext, stateDB, it.varTable, it.preExecutionTable, it.mvCache,
			it.chainConfig, vm.Config{EnablePreimageRecording: false}, false, false, false)
		_, _, err = applyFastPath(msg, tx, vmenv, ret, gp, blockNumber, blockHash, usedGas, it, nil, true)
		if err != nil {
			return fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
	}

	return nil
}

// executeTask one thread executes a transaction with its fast path (under concurrent execution)
func (it *ICSEThread) executeTaskFastPath(task *minHeap.ReadyItem, PrintDetails bool) ([]*state.ReadLoc, state.WriteSets, *types.Receipt, []*types.Log) {
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
	// create evm
	ret, _ := it.preExecutionTable.GetResult(tx.Hash())
	txContext := ret.GetTxContext()
	msg, err := TransactionToMessage(tx, types.MakeSigner(it.chainConfig, header.Number), header.BaseFee)
	if err != nil {
		log.Panic(fmt.Errorf("could not format transaction %+v to message", task))
	}
	vmenv := vm.NewEVM2(it.blockContext, txContext, it.txStateDB, it.varTable, it.preExecutionTable, it.mvCache,
		it.chainConfig, vm.Config{EnablePreimageRecording: false}, false, false, false)
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

	receipt, _, err := applyFastPath(msg, tx, vmenv, ret, gp, blockNumber, blockHash, usedGas, it, nil, true)
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

	return readSet, writeSet, receipt, receipt.Logs
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
	vmenv := vm.NewEVM(it.blockContext, vm.TxContext{}, it.txStateDB, it.chainConfig, vm.Config{EnablePreimageRecording: false}, false, false, false)
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

// FinalizeBlock conducts cache sweep, state finalise, and rewards distribution after execution
func (it *ICSEThread) FinalizeBlock(stateDB *state.IcseStateDB) (common.Hash, error) {
	// 更新分支历史与感知器
	err := it.UpdateBranchHistory()
	if err != nil {
		return common.Hash{}, err
	}
	// 在预执行表中设置上链标识
	for _, tx := range it.block.Transactions() {
		it.preExecutionTable.SetOnChain(tx.Hash())
	}
	// 清理分支变量表与预执行表的冗余项
	it.varTable.Sweep()
	it.preExecutionTable.ClearTable()

	// Fail if Shanghai not enabled and len(withdrawals) is non-zero.
	withdrawals := it.block.Withdrawals()
	if len(withdrawals) > 0 && !it.chainConfig.IsShanghai(it.block.Time()) {
		return common.Hash{}, fmt.Errorf("withdrawals before shanghai")
	}
	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	accumulateRewards2(it.chainConfig, stateDB, it.block.Header(), it.block.Uncles())
	stateDB.FinaliseMVMemory()
	//root := stateDB.IntermediateRoot(it.chainConfig.IsEIP158(it.block.Header().Number))
	root, err2 := stateDB.Commit(it.chainConfig.IsEIP158(it.block.Header().Number))
	if err2 != nil {
		return common.Hash{}, err2
	}
	return root, nil
}

// OutputRWSets outputs read/write sets of pre-executed transactions
func (it *ICSEThread) OutputRWSets() (map[common.Hash]vm.ReadSet, map[common.Hash]vm.WriteSet) {
	var readSets = make(map[common.Hash]vm.ReadSet)
	var writeSets = make(map[common.Hash]vm.WriteSet)
	for _, tx := range it.block.Transactions() {
		ret, _ := it.preExecutionTable.GetResult(tx.Hash())
		rs, ws := ret.OutputRWSet()
		readSets[tx.Hash()] = rs
		writeSets[tx.Hash()] = ws
	}
	return readSets, writeSets
}

// UpdateBranchHistory updates branch history info
func (it *ICSEThread) UpdateBranchHistory() error {
	for _, tx := range it.block.Transactions() {
		ret, _ := it.preExecutionTable.GetResult(tx.Hash())
		brs := ret.GetBranches()
		if len(brs) > 0 {
			for _, br := range brs {
				sUnit, _ := br.GetStateUnit().(*vm.StateUnit)
				if br.GetFilled() && strings.Compare(sUnit.GetBlockEnv(), "nil") == 0 {
					if err := it.varTable.AddHistory(br); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (it *ICSEThread) IncrementTotal()              { it.totalPredictions++ }
func (it *ICSEThread) IncrementUnsatisfied()        { it.unsatisfiedPredictions++ }
func (it *ICSEThread) IncrementSatisfiedTxs()       { it.satisfiedTxs++ }
func (it *ICSEThread) UpdateBlock(blk *types.Block) { it.block = blk }
func (it *ICSEThread) SetChannels(source <-chan []*types.Transaction, disorder <-chan *types.Transaction, txMap <-chan map[common.Hash]*types.Transaction, tip <-chan map[common.Hash]*big.Int, blkChannel <-chan *types.Block) {
	it.txSource = source
	it.disorder = disorder
	it.txSet = txMap
	it.tip = tip
	it.blkChannel = blkChannel
}
func (it *ICSEThread) GetPredictionResults() (int, int, int) {
	return it.totalPredictions, it.unsatisfiedPredictions, it.satisfiedTxs
}
func (it *ICSEThread) GetVarTable() *vm.VarTable                   { return it.varTable }
func (it *ICSEThread) GetMVCache() *state.MVCache                  { return it.mvCache }
func (it *ICSEThread) GetPreExecutionTable() *vm.PreExecutionTable { return it.preExecutionTable }

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
