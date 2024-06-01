package experiments

import (
	"bufio"
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	math2 "github.com/ethereum/go-ethereum/common/math"
	"math"
	"math/big"
	"os"
	"prophetEVM/client"
	"prophetEVM/config"
	"prophetEVM/core"
	"prophetEVM/core/state"
	"prophetEVM/core/state/snapshot"
	"prophetEVM/core/types"
	"prophetEVM/core/vm"
	"prophetEVM/database"
	"prophetEVM/dependencyGraph"
	"prophetEVM/minHeap"
	"sync"
	"time"
)

const exp_branch_statistics = "./experiments/branch_statistics.txt"
const exp_prediction_ratio200 = "./experiments/prediction_200.txt"
const exp_prediciton_ratio400 = "./experiments/prediction_400.txt"
const exp_prediciton_ratio600 = "./experiments/prediction_600.txt"
const exp_prediciton_ratio800 = "./experiments/prediction_800.txt"
const exp_prediciton_ratio1000 = "./experiments/prediction_1000.txt"
const exp_speedup_perTx = "./experiments/speedup_perTx.txt"
const exp_concurrent_speedup = "./experiments/concurrent_speedup.txt"
const exp_prediction_breakdown = "./experiments/prediction_breakdown.txt"
const exp_prexecution_breakdown = "./experiments/prexecution_breakdown.txt"
const exp_speedup_breakdown = "./experiments/speedup_breakdown.txt"

func TestBranchStatistics(startingHeight, offset int64) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
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
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
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
		// EVM执行所需要的区块上下文，一次性生成且后续不能改动
		blockContext := core.NewEVMBlockContext(block.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, block, blockContext, config.MainnetChainConfig)

		txSet := make(map[common.Hash]*types.Transaction)
		tipMap := make(map[common.Hash]*big.Int)
		for _, tx := range block.Transactions() {
			tip := math2.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
			txSet[tx.Hash()] = tx
			tipMap[tx.Hash()] = tip
		}

		t2 := time.Now()
		newThread.PreExecution(txSet, tipMap, true, true, true)
		e2 := time.Since(t2)
		fmt.Printf("Pre-execution latency is: %s\n", e2)

		_, _, _, _, _, err = newThread.FastExecution(stateDb, nil, true, true, true, exp_branch_statistics)
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

func TestPreExecutionLarge(txNum int, startingHeight, offset int64, ratio float64) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight+1))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header = blockPre.Header()
		parentRoot *common.Hash  = &parent.Root
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
		// EVM执行所需要的区块上下文，一次性生成且后续不能改动
		blockContext = core.NewEVMBlockContext(startBlock.Header(), db, nil)
		txSource     = make(chan []*types.Transaction)
		disorder     = make(chan *types.Transaction)
		txMap        = make(chan map[common.Hash]*types.Transaction)
		tip          = make(chan map[common.Hash]*big.Int)
		block        = make(chan *types.Block)
		wg           sync.WaitGroup
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()
	newThread := core.NewThread(0, stateDb, nil, nil, nil, nil, branchTable, mvCache, preTable, startBlock, blockContext, config.MainnetChainConfig)
	newThread.SetChannels(txSource, disorder, txMap, tip, block)

	// 创建交易分发客户端
	cli := client.NewFakeClient(txSource, disorder, txMap, tip, block)

	wg.Add(2)
	go func() {
		defer wg.Done()
		cli.Run(db, txNum, startingHeight+1, offset, false, true, nil, ratio)
	}()
	go func() {
		defer wg.Done()
		newThread.PreExecutionWithDisorder(true, true, true)
	}()
	wg.Wait()

	return nil
}

func TestPredictionSuccess(startingHeight, offset int64, ratio float64) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent           *types.Header  = startBlock.Header()
		parentRoot       *common.Hash   = &parent.Root
		stateCache       state.Database = database.NewStateCache(db)
		snaps            *snapshot.Tree = database.NewSnap(db, stateCache, startBlock.Header())
		txSource                        = make(chan []*types.Transaction)
		disorder                        = make(chan *types.Transaction)
		txMap                           = make(chan map[common.Hash]*types.Transaction)
		tip                             = make(chan map[common.Hash]*big.Int)
		block                           = make(chan *types.Block)
		wg               sync.WaitGroup
		expData          [][]float64 = make([][]float64, 0)
		totalTxRatio     float64
		totalBranchRatio float64
		validBlockNum    int
		fileName         string
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	serialDB, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		blk, err2 := database.GetBlockByNumber(db, i)
		if err2 != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err2)
		}

		// 首先去除I/O的影响，先执行加载状态数据到内存
		_, _, _, _, _, _ = serialProcessor.Process(blk, serialDB, vm.Config{EnablePreimageRecording: false}, nil)
		_, _ = serialDB.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))

		preStateDB := stateDb.Copy()
		blockContext := core.NewEVMBlockContext(blk.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, blk, blockContext, config.MainnetChainConfig)
		newThread.SetChannels(txSource, disorder, txMap, tip, block)

		// 创建交易分发客户端
		cli := client.NewFakeClient(txSource, disorder, txMap, tip, block)

		wg.Add(2)
		go func() {
			defer wg.Done()
			cli.Run(db, 0, i.Int64(), 0, false, false, nil, ratio)
		}()
		go func() {
			defer wg.Done()
			newThread.PreExecutionWithDisorder(true, true, true)
		}()
		wg.Wait()

		_, _, _, _, ctxNum, err := newThread.FastExecution(stateDb, nil, true, true, false, "")
		if err != nil {
			fmt.Println("execution error", err)
		}
		// Commit all cached state changes into underlying memory database.
		root, _ := stateDb.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))

		total, unsatisfied, satisfiedTxs := newThread.GetPredictionResults()
		txRatio := float64(satisfiedTxs) / float64(ctxNum)
		branchRatio := float64(total-unsatisfied) / float64(total)
		if ctxNum != 0 && total != 0 {
			totalTxRatio += txRatio
			totalBranchRatio += branchRatio
			expData = append(expData, []float64{txRatio, branchRatio})
			validBlockNum++
		}
		fmt.Printf("Ratio of satisfied branch dircetions: %.2f, ratio of satisfied txs: %.2f\n", branchRatio, txRatio)
		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root)

		parent = blk.Header()
		// metadata reference to keep trie alive
		//serialDB.Database().TrieDB().Reference(root0, common.Hash{})
		//snaps = database.NewSnap(db, stateCache, blk.Header())
		//serialDB, _ = state.New(root0, stateCache, snaps)
		//serialDB.Database().TrieDB().Dereference(parentRoot)
		//parentRoot = root0

		// write to the experimental script
		tmpBlockNum := i.Int64() - startingHeight
		if tmpBlockNum%200 == 0 {
			avgTxRatio := totalTxRatio / float64(validBlockNum)
			avgBranchRatio := totalBranchRatio / float64(validBlockNum)
			expData = append(expData, []float64{avgTxRatio, avgBranchRatio})

			switch tmpBlockNum {
			case 200:
				fileName = exp_prediction_ratio200
			case 400:
				fileName = exp_prediciton_ratio400
			case 600:
				fileName = exp_prediciton_ratio600
			case 800:
				fileName = exp_prediciton_ratio800
			case 1000:
				fileName = exp_prediciton_ratio1000
			}

			file, err := os.OpenFile(fileName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
			if err != nil {
				fmt.Printf("open error: %v\n", err)
			}
			defer file.Close()

			writer := bufio.NewWriter(file)
			for _, row := range expData {
				_, err = fmt.Fprintf(writer, "%.2f %.2f\n", row[0], row[1])
				if err != nil {
					fmt.Printf("write error: %v\n", err)
					return nil
				}
			}

			err = writer.Flush()
			if err != nil {
				fmt.Printf("flush error: %v\n", err)
				return nil
			}

			totalTxRatio = 0
			totalBranchRatio = 0
			validBlockNum = 0
			expData = make([][]float64, 0)
		}
	}

	return nil
}

func TestSpeedupPerTx(startingHeight, offset int64, ratio float64, enableRepair, enablePerceptron, enableFast bool, fileName string) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent        *types.Header  = startBlock.Header()
		parentRoot    *common.Hash   = &parent.Root
		stateCache    state.Database = database.NewStateCache(db)
		snaps         *snapshot.Tree = database.NewSnap(db, stateCache, startBlock.Header())
		txSource                     = make(chan []*types.Transaction)
		disorder                     = make(chan *types.Transaction)
		txMap                        = make(chan map[common.Hash]*types.Transaction)
		tip                          = make(chan map[common.Hash]*big.Int)
		block                        = make(chan *types.Block)
		wg            sync.WaitGroup
		totalSpeedups int
		largeRatio    float64
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	serialDB, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	// 新建性能观察器
	recorder := core.NewRecorder()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		blk, err2 := database.GetBlockByNumber(db, i)
		if err2 != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err2)
		}

		// 首先去除I/O的影响，先执行加载状态数据到内存
		_, _, _, _, _, _ = serialProcessor.Process(blk, serialDB.Copy(), vm.Config{EnablePreimageRecording: false}, nil)
		_, _, _, _, _, _ = serialProcessor.Process(blk, serialDB, vm.Config{EnablePreimageRecording: false}, recorder)
		root0, _ := serialDB.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))

		preStateDB := stateDb.Copy()
		blockContext := core.NewEVMBlockContext(blk.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, blk, blockContext, config.MainnetChainConfig)
		newThread.SetChannels(txSource, disorder, txMap, tip, block)

		// 创建交易分发客户端
		cli := client.NewFakeClient(txSource, disorder, txMap, tip, block)

		wg.Add(2)
		go func() {
			defer wg.Done()
			cli.Run(db, 0, i.Int64(), 0, false, false, nil, ratio)
		}()
		go func() {
			defer wg.Done()
			newThread.PreExecutionWithDisorder(true, enableRepair, enablePerceptron)
		}()
		wg.Wait()

		_, _, _, _, _, err := newThread.FastExecution(stateDb, recorder, enablePerceptron, enableFast, false, "")
		if err != nil {
			fmt.Println("execution error", err)
		}
		// Commit all cached state changes into underlying memory database.
		root, _ := stateDb.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))
		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root)

		// reset stateDB every epoch to remove accumulated I/O overhead
		snaps = database.NewSnap(db, stateCache, blk.Header())
		serialDB, _ = state.New(root0, stateCache, snaps)
		stateDb, _ = state.New(root, stateCache, snaps)
	}

	if fileName == "" {
		fileName = exp_speedup_perTx
	}
	file, err := os.OpenFile(fileName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("open error: %v\n", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	speedupMap := recorder.SpeedupCalculation()
	totalTxNum := recorder.GetValidTxNum()

	for speedup, num := range speedupMap {
		if speedup >= 200 {
			//totalSpeedups += 50 * num
			totalTxNum -= num
		} else {
			totalSpeedups += speedup * num
		}
		rat := float64(num) / float64(recorder.GetValidTxNum())
		if speedup >= 50 {
			largeRatio += rat
		} else {
			_, err = fmt.Fprintf(writer, "%d %.4f\n", speedup, rat)
			if err != nil {
				fmt.Printf("write error: %v\n", err)
				return nil
			}
		}
	}

	_, err = fmt.Fprintf(writer, ">=50 %.4f\n", largeRatio)
	if err != nil {
		fmt.Printf("write error: %v\n", err)
		return nil
	}
	_, err = fmt.Fprintf(writer, "Average speedup: %.4f\n", float64(totalSpeedups)/float64(totalTxNum))
	if err != nil {
		fmt.Printf("write error: %v\n", err)
		return nil
	}

	err = writer.Flush()
	if err != nil {
		fmt.Printf("flush error: %v\n", err)
		return nil
	}

	return nil
}

func TestSeerBreakDown(startingHeight, offset int64, ratio float64, enableRepair, enablePerceptron, enableFast bool) error {
	filePrediction, err := os.OpenFile(exp_prediction_breakdown, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("open error: %v\n", err)
	}
	defer filePrediction.Close()

	filePreExecution, err := os.OpenFile(exp_prexecution_breakdown, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("open error: %v\n", err)
	}
	defer filePreExecution.Close()

	fileSpeedup, err := os.OpenFile(exp_speedup_breakdown, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("open error: %v\n", err)
	}
	defer fileSpeedup.Close()

	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	var (
		largeBlock         *types.Block
		count              int
		combinedTxs        types.Transactions
		txSource           = make(chan []*types.Transaction)
		disorder           = make(chan *types.Transaction)
		txMap              = make(chan map[common.Hash]*types.Transaction)
		tip                = make(chan map[common.Hash]*big.Int)
		block              = make(chan *types.Block)
		wg                 sync.WaitGroup
		predictionData     [][]float64 = make([][]float64, 0)
		preExecutionData   [][]float64 = make([][]float64, 0)
		totalTxRatio       float64
		totalBranchRatio   float64
		preLatencyPerBlock float64
		totalPreLatency    float64
		totalSpeedups      int
		largeRatio         float64
		validBlockNum      int
	)

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header  = startBlock.Header()
		parentRoot *common.Hash   = &parent.Root
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, startBlock.Header())
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)
	serialDB, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()
	// 新建性能观察器
	recorder := core.NewRecorder()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		count++
		blk, err2 := database.GetBlockByNumber(db, i)
		if err2 != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err2)
		}
		if count == 1 {
			largeBlock = blk
		}
		combinedTxs = append(combinedTxs, blk.Transactions()...)

		// 每隔十个区块组合一次
		if count == 10 {
			largeBlock.AddTransactions(combinedTxs)
			preStateDB := stateDb.Copy()
			blockContext := core.NewEVMBlockContext(largeBlock.Header(), db, nil)
			newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, largeBlock, blockContext, config.MainnetChainConfig)
			newThread.SetChannels(txSource, disorder, txMap, tip, block)

			// 首先去除I/O的影响，先执行加载状态数据到内存
			_, _, _, _, _, _ = serialProcessor.Process(largeBlock, serialDB.Copy(), vm.Config{EnablePreimageRecording: false}, nil)
			_, _, _, _, _, _ = serialProcessor.Process(largeBlock, serialDB, vm.Config{EnablePreimageRecording: false}, recorder)
			root0, _ := serialDB.Commit(config.MainnetChainConfig.IsEIP158(largeBlock.Number()))

			// 创建交易分发客户端
			cli := client.NewFakeClient(txSource, disorder, txMap, tip, block)

			wg.Add(2)
			go func() {
				defer wg.Done()
				cli.Run(db, 0, i.Int64(), 0, true, false, largeBlock, ratio)
			}()
			go func() {
				defer wg.Done()
				lat := newThread.PreExecutionWithDisorder(true, enableRepair, enablePerceptron)
				preLatencyPerBlock = float64(lat.Microseconds()) / float64(10000)
				totalPreLatency += preLatencyPerBlock
				preExecutionData = append(preExecutionData, []float64{float64(i.Int64()), preLatencyPerBlock})
			}()
			wg.Wait()

			_, _, _, _, ctxNum, err := newThread.FastExecution(stateDb, recorder, enablePerceptron, enableFast, false, "")
			if err != nil {
				fmt.Println("execution error", err)
			}
			// Commit all cached state changes into underlying memory database.
			root, _ := stateDb.Commit(config.MainnetChainConfig.IsEIP158(largeBlock.Number()))

			total, unsatisfied, satisfiedTxs := newThread.GetPredictionResults()
			txRatio := float64(satisfiedTxs) / float64(ctxNum)
			branchRatio := float64(total-unsatisfied) / float64(total)
			if ctxNum != 0 && total != 0 {
				totalTxRatio += txRatio
				totalBranchRatio += branchRatio
				predictionData = append(predictionData, []float64{txRatio, branchRatio})
				validBlockNum++
			}
			fmt.Printf("Ratio of satisfied branch dircetions: %.2f, ratio of satisfied txs: %.2f\n", branchRatio, txRatio)
			fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay large block number "+i.String(), root)

			// reset stateDB every epoch to remove accumulated I/O overhead
			snaps = database.NewSnap(db, stateCache, blk.Header())
			serialDB, _ = state.New(root0, stateCache, snaps)
			stateDb, _ = state.New(root, stateCache, snaps)

			count = 0
			combinedTxs = []*types.Transaction{}
		}
	}

	// write the prediction results to the script
	avgTxRatio := totalTxRatio / float64(validBlockNum)
	avgBranchRatio := totalBranchRatio / float64(validBlockNum)
	predictionData = append(predictionData, []float64{avgTxRatio, avgBranchRatio})

	writer := bufio.NewWriter(filePrediction)
	for _, row := range predictionData {
		_, err = fmt.Fprintf(writer, "%.3f %.3f\n", row[0], row[1])
		if err != nil {
			fmt.Printf("write error: %v\n", err)
			return nil
		}
	}

	// write the prediction results to the experimental script
	avgLatency := totalPreLatency / float64(offset/10)
	preExecutionData = append(preExecutionData, []float64{float64(offset), avgLatency})

	writer2 := bufio.NewWriter(filePreExecution)
	for _, row := range preExecutionData {
		_, err = fmt.Fprintf(writer2, "%.3f %.3f\n", row[0], row[1])
		if err != nil {
			fmt.Printf("write error: %v\n", err)
			return nil
		}
	}

	writer3 := bufio.NewWriter(fileSpeedup)
	speedupMap := recorder.SpeedupCalculation()

	for speedup, num := range speedupMap {
		if speedup == 0 {
			totalSpeedups += int(math.Ceil(float64(num) * 0.5))
		} else {
			totalSpeedups += speedup * num
		}
		rat := float64(num) / float64(recorder.GetValidTxNum())
		if speedup >= 50 {
			largeRatio += rat
		} else {
			_, err = fmt.Fprintf(writer3, "%d %.4f\n", speedup, rat)
			if err != nil {
				fmt.Printf("write error: %v\n", err)
				return nil
			}
		}
	}
	_, err = fmt.Fprintf(writer3, ">=50 %.4f\n", largeRatio)
	if err != nil {
		fmt.Printf("write error: %v\n", err)
		return nil
	}
	_, err = fmt.Fprintf(writer3, "Average speedup: %.4f\n", float64(totalSpeedups)/float64(recorder.GetValidTxNum()))
	if err != nil {
		fmt.Printf("write error: %v\n", err)
		return nil
	}

	err = writer.Flush()
	if err != nil {
		fmt.Printf("flush error: %v\n", err)
		return nil
	}
	err = writer2.Flush()
	if err != nil {
		fmt.Printf("flush error: %v\n", err)
		return nil
	}
	err = writer3.Flush()
	if err != nil {
		fmt.Printf("flush error: %v\n", err)
		return nil
	}

	return nil
}

// TestSeerConcurrentLarge evaluates pre-execution and fast-path concurrent execution using the large block
func TestSeerConcurrentLarge(threads, txNum int, startingHeight, offset int64) error {
	file, err := os.OpenFile(exp_concurrent_speedup, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("open error: %v\n", err)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)

	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight+1))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header = blockPre.Header()
		parentRoot *common.Hash  = &parent.Root
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache    state.Database = database.NewStateCache(db)
		snaps         *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
		readSets                     = make(map[common.Hash]vm.ReadSet)
		writeSets                    = make(map[common.Hash]vm.WriteSet)
		txLen         int
		serialLatency int64
	)

	// 新建原生stateDB，用于串行执行测试
	nativeDb, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	var concurrentTxs types.Transactions
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}

		preStateDB := nativeDb.Copy()
		blockContext := core.NewEVMBlockContext(block.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, block, blockContext, config.MainnetChainConfig)

		newTxs := block.Transactions()
		if txLen+len(newTxs) >= txNum {
			var remainingTxs types.Transactions
			for j := 0; j < txNum-txLen; j++ {
				concurrentTxs = append(concurrentTxs, newTxs[j])
				remainingTxs = append(remainingTxs, newTxs[j])
			}
			txSet := make(map[common.Hash]*types.Transaction)
			tipMap := make(map[common.Hash]*big.Int)
			for _, tx := range remainingTxs {
				tip := math2.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
				txSet[tx.Hash()] = tx
				tipMap[tx.Hash()] = tip
			}
			block.AddTransactions(remainingTxs)
			newThread.UpdateBlock(block)
			rSets, wSets := newThread.PreExecution(txSet, tipMap, true, true, true)
			for id, read := range rSets {
				readSets[id] = read
			}
			for id, write := range wSets {
				writeSets[id] = write
			}
			//_, _, _, _, _, _ = serialProcessor.Process(block, nativeDb.Copy(), vm.Config{EnablePreimageRecording: false}, nil)
			s := time.Now()
			_, _, _, _, _, _ = serialProcessor.Process(block, nativeDb, vm.Config{EnablePreimageRecording: false}, nil)
			e := time.Since(s)
			serialLatency += e.Microseconds()
			_, _ = nativeDb.Commit(config.MainnetChainConfig.IsEIP158(startBlock.Number()))
			break
		} else {
			txSet := make(map[common.Hash]*types.Transaction)
			tipMap := make(map[common.Hash]*big.Int)
			for _, tx := range newTxs {
				tip := math2.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
				txSet[tx.Hash()] = tx
				tipMap[tx.Hash()] = tip
			}
			rSets, wSets := newThread.PreExecution(txSet, tipMap, true, true, true)
			for id, read := range rSets {
				readSets[id] = read
			}
			for id, write := range wSets {
				writeSets[id] = write
			}
			//_, _, _, _, _, _ = serialProcessor.Process(block, nativeDb.Copy(), vm.Config{EnablePreimageRecording: false}, nil)
			s := time.Now()
			_, _, _, _, _, _ = serialProcessor.Process(block, nativeDb, vm.Config{EnablePreimageRecording: false}, nil)
			e := time.Since(s)
			serialLatency += e.Microseconds()
			_, _ = nativeDb.Commit(config.MainnetChainConfig.IsEIP158(startBlock.Number()))
		}
		concurrentTxs = append(concurrentTxs, newTxs...)
		txLen += len(newTxs)
	}
	startBlock.AddTransactions(concurrentTxs)

	fmt.Printf("Serial execution time: %.2f\n", float64(serialLatency)/float64(1000000))
	fmt.Fprintf(writer, "Serial execution time: %.2f\n", float64(serialLatency)/float64(1000000))

	// 构建依赖图
	blockContext := core.NewEVMBlockContext(startBlock.Header(), db, nil)
	newThread := core.NewThread(0, nativeDb, nil, nil, nil, nil, branchTable, mvCache, preTable, startBlock, blockContext, config.MainnetChainConfig)

	dg := dependencyGraph.ConstructDependencyGraph(readSets, writeSets, startBlock.Transactions())
	// 尚不可执行的交易队列（因为storage<next，所以尚不可执行）
	Htxs := minHeap.NewTxsHeap()
	// 已经可以执行，但是处于等待状态的交易队列
	Hready := minHeap.NewReadyHeap()
	// 执行完毕，等待验证的交易队列
	Hcommit := minHeap.NewCommitHeap()
	// 执行DCC-DA算法

	// 新建并发执行所需的数据库IcseStateDB
	stateDb, _ := state.NewIcseStateDB(*parentRoot, stateCache, snaps)
	ctx, cancel := context.WithCancel(context.Background())
	for j := 1; j <= threads; j++ {
		go func(threadID int) {
			thread := core.NewThread(threadID, nil, stateDb, nil, Hready, Hcommit, branchTable, mvCache, preTable, startBlock, blockContext, config.MainnetChainConfig)
			thread.Run(ctx, true, false)
		}(j)
	}
	duration := core.DCCDA(startBlock.Transactions().Len(), Htxs, Hready, Hcommit, stateDb, dg)
	fmt.Fprintf(writer, "Concurrent execution latency：%s\n", duration)
	cancel()

	// Commit all cached state changes into underlying memory database.
	_, err2 := newThread.FinalizeBlock(stateDb)
	if err2 != nil {
		return fmt.Errorf("finalize block error: %s", err2)
	}

	speedup := float64(serialLatency) / float64(duration.Microseconds())
	fmt.Fprintf(writer, "Speedup is：%.2f\n", speedup)

	err = writer.Flush()
	if err != nil {
		fmt.Printf("flush error: %v\n", err)
		return nil
	}

	return nil
}

func TestMemoryBreakDown(startingHeight, offset int64, enablePerceptron, enableFast, storeCheckpoint bool) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header  = startBlock.Header()
		parentRoot *common.Hash   = &parent.Root
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, startBlock.Header())
	)

	// 新建原生数据库
	stateDb, _ := state.New(*parentRoot, stateCache, snaps)

	// 构建预测用的内存结构
	branchTable := vm.CreateNewTable()
	mvCache := state.NewMVCache(10, 0.1)
	preTable := vm.NewPreExecutionTable()

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		blk, err2 := database.GetBlockByNumber(db, i)
		if err2 != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err2)
		}

		preStateDB := stateDb.Copy()
		blockContext := core.NewEVMBlockContext(blk.Header(), db, nil)
		newThread := core.NewThread(0, preStateDB, nil, nil, nil, nil, branchTable, mvCache, preTable, blk, blockContext, config.MainnetChainConfig)

		txSet := make(map[common.Hash]*types.Transaction)
		tipMap := make(map[common.Hash]*big.Int)
		for _, tx := range blk.Transactions() {
			tip := math2.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), blk.BaseFee()))
			txSet[tx.Hash()] = tx
			tipMap[tx.Hash()] = tip
		}
		newThread.PreExecution(txSet, tipMap, true, enablePerceptron, storeCheckpoint)

		_, _, _, _, _, err := newThread.FastExecution(stateDb, nil, enablePerceptron, enableFast, false, "")
		if err != nil {
			fmt.Println("execution error", err)
		}
		// Commit all cached state changes into underlying memory database.
		root, _ := stateDb.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))
		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root)
	}

	return nil
}

func TestMemoryBaseline(startingHeight, offset int64) error {
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetInt64(startingHeight))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}

	var (
		parent     *types.Header  = startBlock.Header()
		parentRoot *common.Hash   = &parent.Root
		stateCache state.Database = database.NewStateCache(db)
		snaps      *snapshot.Tree = database.NewSnap(db, stateCache, startBlock.Header())
	)

	serialDB, _ := state.New(*parentRoot, stateCache, snaps)
	serialProcessor := core.NewStateProcessor(config.MainnetChainConfig, db)

	min, max, addSpan := big.NewInt(startingHeight+1), big.NewInt(startingHeight+offset+1), big.NewInt(1)
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		blk, err2 := database.GetBlockByNumber(db, i)
		if err2 != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err2)
		}
		_, _, _, _, _, _ = serialProcessor.Process(blk, serialDB, vm.Config{EnablePreimageRecording: false}, nil)
		root0, _ := serialDB.Commit(config.MainnetChainConfig.IsEIP158(blk.Number()))
		fmt.Println("["+time.Now().Format("2006-01-02 15:04:05")+"]", "successfully replay block number "+i.String(), root0)
		time.Sleep(200 * time.Millisecond)
	}

	return nil
}
