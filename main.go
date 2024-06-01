package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"math/big"
	"prophetEVM/config"
	"prophetEVM/core"
	"prophetEVM/core/state"
	"prophetEVM/core/state/snapshot"
	"prophetEVM/core/types"
	"prophetEVM/core/vm"
	"prophetEVM/database"
	"prophetEVM/dependencyGraph"
	"prophetEVM/experiments"
	"prophetEVM/minHeap"
	"runtime"
	"time"
)

const testTxsLen = 5000

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	//experiments.TestPreExecutionLarge(2000, 14650000, 200, 0.4)
	//experiments.TestPredictionSuccess(14650000, 1000, 0)

	//experiments.TestBranchStatistics(14000000, 1000)

	//experiments.TestSpeedupPerTx(14650000, 1000, 0.2, true, true, true, "./experiments/speedup_perTx_20_full.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.3, true, true, true, "./experiments/speedup_perTx_30_full.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.1, true, true, true, "./experiments/speedup_perTx_10_full.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.4, true, true, true, "./experiments/speedup_perTx_40_full.txt")
	//
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.1, true, true, false, "./experiments/speedup_perTx_10_perceptron.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.2, true, true, false, "./experiments/speedup_perTx_20_perceptron.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.3, true, true, false, "./experiments/speedup_perTx_30_perceptron.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.4, true, true, false, "./experiments/speedup_perTx_40_perceptron.txt")
	//
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.1, true, false, false, "./experiments/speedup_perTx_10_repair.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.2, true, false, false, "./experiments/speedup_perTx_20_repair.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.3, true, false, false, "./experiments/speedup_perTx_30_repair.txt")
	//experiments.TestSpeedupPerTx(14650000, 1000, 0.4, true, false, false, "./experiments/speedup_perTx_40_repair.txt")

	//experiments.TestSeerConcurrentLarge(2, 10000, 14650000, 500)
	//experiments.TestSeerBreakDown(14650000, 1000, 0.1, false, false, false)

	//experiments.TestMemoryBaseline(14650000, 1000)
	experiments.TestMemoryBreakDown(14650000, 1000, false, false, false)
}

// TestSeerEVM evaluates pre-execution and fast-path execution with a large block including concurrent transactions
func TestSeerEVM() error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14000000))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	txLen := 0
	min, max, addSpan := big.NewInt(14000001), big.NewInt(14000020), big.NewInt(1)
	var concurrentTxs types.Transactions
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}
		newTxs := block.Transactions()
		if txLen+len(newTxs) >= testTxsLen {
			for j := 0; j < testTxsLen-txLen; j++ {
				concurrentTxs = append(concurrentTxs, newTxs[j])
			}
			break
		}
		concurrentTxs = append(concurrentTxs, newTxs...)
		txLen += len(newTxs)
	}
	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14000001))
	if err != nil {
		return fmt.Errorf("get block 14000001 error: %s", err)
	}
	startBlock.AddTransactions(concurrentTxs) // 构造出一个区块拥有810~850区块中的所有交易

	var (
		parent     *types.Header = blockPre.Header()
		parentRoot *common.Hash  = &parent.Root
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
		// EVM执行所需要的区块上下文，一次性生成且后续不能改动
		blockContext = core.NewEVMBlockContext(startBlock.Header(), db, nil)
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	// 创建用于真正执行的stateDB
	executionStateDB := stateDb.Copy()
	// 新建所有交易共用的数据库stateDB，储存多版本stateobject
	stateDb2, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps) // IcseStateDB类型，表征一个状态，需要由db变量构造而来
	if stateDb2 == nil {
		return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
	}
	// 再准备一个状态数据库，用于模拟执行交易从而生成依赖图
	//stateDbForConstruction := stateDb.Copy()
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()
	newThread := core.NewThread(0, stateDb, stateDb2, nil, nil, nil, branchTable, mvCache, preTable, startBlock, blockContext, config.MainnetChainConfig)

	txSet := make(map[common.Hash]*types.Transaction)
	tipMap := make(map[common.Hash]*big.Int)
	for _, tx := range startBlock.Transactions() {
		tip := math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), startBlock.BaseFee()))
		txSet[tx.Hash()] = tx
		tipMap[tx.Hash()] = tip
	}

	t1 := time.Now()
	newThread.PreExecution(txSet, tipMap, true, true, true)
	e1 := time.Since(t1)
	fmt.Printf("Pre-execution latency is: %s\n", e1)

	root, _, _, _, _, err := newThread.FastExecution(executionStateDB, nil, true, true, false, "")
	if err != nil {
		fmt.Println("execution error", err)
	}
	total, unsatisfied, _ := newThread.GetPredictionResults()
	fmt.Printf("Ratio of satisfied branch dircetions: %.2f\n", float64(total-unsatisfied)/float64(total))
	fmt.Println(root)

	return nil
}

// TestSeerEVMBlockByBlock evaluates pre-execution and fast-path execution using the native block (in a specific block height interval)
func TestSeerEVMBlockByBlock() error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14650000))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header = blockPre.Header()
		parentRoot *common.Hash  = &parent.Root
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
	)

	// 新建原生数据库
	serialDB, _ := state.New(*parentRoot, stateCache, snaps)
	if serialDB == nil {
		return errors.New("nil stateDB")
	}
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	if stateDb == nil {
		return errors.New("nil stateDB")
	}
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(14650001), big.NewInt(14650501), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}

		// 测试串行执行时延
		// 首先去除I/O的影响，先执行加载状态数据到内存
		_, _, _, _, _, _ = serialProcessor.Process(block, serialDB.Copy(), vm.Config{EnablePreimageRecording: false}, nil)
		s1 := time.Now()
		_, _, _, _, _, _ = serialProcessor.Process(block, serialDB, vm.Config{EnablePreimageRecording: false}, nil)
		e1 := time.Since(s1)
		fmt.Printf("Serial execution time: %s\n", e1)
		_, _ = serialDB.Commit(config.MainnetChainConfig.IsEIP158(block.Number()))

		// 创建用于预测执行的stateDB
		preStateDB := stateDb.Copy()
		stateDb2, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps) // IcseStateDB类型，表征一个状态，需要由db变量构造而来
		if stateDb2 == nil {
			return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
		}

		// EVM执行所需要的区块上下文，一次性生成且后续不能改动
		blockContext := core.NewEVMBlockContext(block.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, stateDb2, nil, nil, nil, branchTable, mvCache, preTable, block, blockContext, config.MainnetChainConfig)

		txSet := make(map[common.Hash]*types.Transaction)
		tipMap := make(map[common.Hash]*big.Int)
		for _, tx := range block.Transactions() {
			tip := math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
			txSet[tx.Hash()] = tx
			tipMap[tx.Hash()] = tip
		}

		t2 := time.Now()
		newThread.PreExecution(txSet, tipMap, true, true, true)
		e2 := time.Since(t2)
		fmt.Printf("Pre-execution latency is: %s\n", e2)

		_, _, _, _, _, err = newThread.FastExecution(stateDb, nil, true, true, false, "")
		if err != nil {
			fmt.Println("execution error", err)
		}
		// Commit all cached state changes into underlying memory database.
		root, _ := stateDb.Commit(config.MainnetChainConfig.IsEIP158(block.Number()))
		//total, unsatisfied := newThread.GetPredictionResults()
		//fmt.Printf("Ratio of satisfied branch dircetions: %.2f\n", float64(total-unsatisfied)/float64(total))

		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root)
		parent = block.Header()
	}

	return nil
}

// TestSeerEVMConcurrent evaluates pre-execution and fast-path concurrent execution using the native block (in a specific block height interval)
func TestSeerEVMConcurrent(threads int) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14650000))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header = blockPre.Header()
		parentRoot *common.Hash  = &parent.Root
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
	)

	// 新建原生stateDB，用于串行执行测试
	nativeDb, _ := state.New(*parentRoot, stateCache, snaps)
	if nativeDb == nil {
		return errors.New("nil stateDB")
	}
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 并发执行交易共用的多版本数据库stateDB
	stateDb, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps)
	if stateDb == nil {
		return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
	}
	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(14650001), big.NewInt(14650041), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}

		// 创建用于串行执行和预测执行的stateDB
		serialDB := nativeDb.Copy()
		preStateDB := nativeDb.Copy()
		// 测试串行执行时延
		// 首先去除I/O的影响，先执行加载状态数据到内存
		_, _, _, _, _, _ = serialProcessor.Process(block, serialDB, vm.Config{EnablePreimageRecording: false}, nil)
		s1 := time.Now()
		_, _, _, _, _, _ = serialProcessor.Process(block, nativeDb, vm.Config{EnablePreimageRecording: false}, nil)
		e1 := time.Since(s1)
		fmt.Printf("Serial execution time: %s\n", e1)
		_, _ = nativeDb.Commit(config.MainnetChainConfig.IsEIP158(block.Number()))

		// 创建主线程，负责预执行以及后续缓存清理
		blockContext := core.NewEVMBlockContext(block.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, stateDb, nil, nil, nil, branchTable, mvCache, preTable, block, blockContext, config.MainnetChainConfig)

		txSet := make(map[common.Hash]*types.Transaction)
		tipMap := make(map[common.Hash]*big.Int)
		for _, tx := range block.Transactions() {
			tip := math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
			txSet[tx.Hash()] = tx
			tipMap[tx.Hash()] = tip
		}

		t2 := time.Now()
		readSets, writeSets := newThread.PreExecution(txSet, tipMap, true, true, true)
		e2 := time.Since(t2)
		fmt.Printf("Pre-execution latency is: %s\n", e2)

		// 构建依赖图
		dg := dependencyGraph.ConstructDependencyGraph(readSets, writeSets, block.Transactions())

		// 尚不可执行的交易队列（因为storage<next，所以尚不可执行）
		Htxs := minHeap.NewTxsHeap()
		// 已经可以执行，但是处于等待状态的交易队列
		Hready := minHeap.NewReadyHeap()
		// 执行完毕，等待验证的交易队列
		Hcommit := minHeap.NewCommitHeap()
		// 执行DCC-DA算法

		ctx, cancel := context.WithCancel(context.Background())
		for j := 1; j <= threads; j++ {
			go func(threadID int) {
				thread := core.NewThread(threadID, nil, stateDb, nil, Hready, Hcommit, branchTable, mvCache, preTable, block, blockContext, config.MainnetChainConfig)
				thread.Run(ctx, true, false)
			}(j)
		}
		core.DCCDA(block.Transactions().Len(), Htxs, Hready, Hcommit, stateDb, dg)
		cancel()

		// Commit all cached state changes into underlying memory database.
		root, err2 := newThread.FinalizeBlock(stateDb)
		if err2 != nil {
			return fmt.Errorf("finalize block error: %s", err2)
		}
		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root)
		parent = block.Header()
	}

	return nil
}
