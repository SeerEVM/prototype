package main

import (
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"math/big"
	"os/exec"
	"prophetEVM/config"
	"prophetEVM/core"
	"prophetEVM/core/state"
	"prophetEVM/core/state/snapshot"
	"prophetEVM/core/types"
	"prophetEVM/database"
	"prophetEVM/dependencyGraph"
	"prophetEVM/minHeap"
	"sync"
	"time"
)

// PrintDetails 是否打印详细信息
const PrintDetails = false
const testTxsLen = 10000

func main() {
	//err := TestICSE(10)
	//if err != nil {
	//	panic(err)
	//}
	//runtime.GOMAXPROCS(runtime.NumCPU())
	//TestICSE(20)
	//TestICSEPerfect(20)
	//var mask []byte
	//for i := 0; i < 64/8; i++ {
	//	mask = append(mask, 255)
	//}
	//s := 1000000000000000000
	//integer := new(uint256.Int)
	//b := []byte{255, 255, 255, 255, 255, 255, 255, 255}
	//integer.SetBytes(b)
	////integer.SetUint64(uint64(s))
	//fmt.Println(integer.Hex())
	//fmt.Println(integer.Hex()[:10])
	cmd := exec.Command("python3", "perceptron.py")
	slice := []int{1, 1, 1, 0, 1, 1, 1, 0, 1, 1, 1, 0, 1, 1, 1, 0, 1, 1, 1, 0}
	jsonSlice, _ := json.Marshal(slice)
	cmd.Args = append(cmd.Args, string(jsonSlice))
	s := time.Now()
	output, err := cmd.CombinedOutput()
	e := time.Since(s)
	if err != nil {
		panic(err)
	}
	fmt.Println("Prediction output:", string(output))
	fmt.Printf("time passed: %s", e)
}

func TestICSE(threads int) error {
	// 数据库封装程度由低到高，ethdb.Database是接口类型，rawdb是一个leveldb或pebble加上freezer
	// ethdb.Database是geth的数据库抽象，rawdb包中给出了一个使用leveldb构造的一个接口具体实现实现，属于disk db
	//rawConfig := database.DefaultRawConfig()
	//rawConfig.Path = "./copychain"
	//rawConfig.Ancient = "./copychain/ancient"
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	//blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14000000))
	_, err = database.GetBlockByNumber(db, new(big.Int).SetUint64(14000000))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}
	//集合810~849区块中的所有交易，总共5577个
	//txLen := 0
	min, max, addSpan := big.NewInt(14000001), big.NewInt(14000003), big.NewInt(1)
	//var concurrentTxs types.Transactions
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}

		var feeSort []int
		var feeSortMap = make(map[int][]int)
		newTxs := block.Transactions()

		for k, tx := range newTxs {
			// EIP-1559
			tip := math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), block.BaseFee()))
			//tip := tx.GasTipCap()
			feeSortMap[int(tip.Int64())] = append(feeSortMap[int(tip.Int64())], k)
			feeSort = append(feeSort, int(tip.Int64()))
		}

		//sort.Sort(sort.Reverse(sort.IntSlice(feeSort)))
		fmt.Println(feeSort)
		fmt.Println(feeSortMap)

		//fmt.Println(result)
		//if txLen+len(newTxs) >= testTxsLen {
		//	for j := 0; j < testTxsLen-txLen; j++ {
		//		concurrentTxs = append(concurrentTxs, newTxs[j])
		//	}
		//	break
		//}
		//concurrentTxs = append(concurrentTxs, newTxs...)
		//txLen += len(newTxs)
	}
	//// 取810号区块并将原810~849区块中所有交易放入，构造本项目所用的一个大区块startBlock
	//startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(14000001))
	//if err != nil {
	//	return fmt.Errorf("get block 14000001 error: %s", err)
	//}
	//startBlock.AddTransactions(concurrentTxs) // 构造出一个区块拥有810~850区块中的所有交易
	//
	//var (
	//	parent     *types.Header = blockPre.Header()
	//	parentRoot *common.Hash  = &parent.Root
	//	// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
	//	stateCache state.Database = database.NewStateCache(db)
	//	snaps      *snapshot.Tree = database.NewSnap(db, stateCache, blockPre.Header())
	//	// EVM执行所需要的区块上下文，一次性生成且后续不能改动
	//	blockContext = core.NewEVMBlockContext(startBlock.Header(), db, nil)
	//)
	//
	//// 新建所有交易共用的数据库stateDB，储存多版本stateobject
	//stateDb, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps) // IcseStateDB类型，表征一个状态，需要由db变量构造而来
	//if stateDb == nil {
	//	return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
	//}
	//// 再准备一个状态数据库，用于模拟执行交易从而生成依赖图
	//stateDbForConstruction := stateDb.Copy()
	//
	//// 尚不可执行的交易队列（因为storage<next，所以尚不可执行）
	//Htxs := minHeap.NewTxsHeap()
	//// 已经可以执行，但是处于等待状态的交易队列
	//Hready := minHeap.NewReadyHeap()
	//// 执行完毕，等待验证的交易队列
	//Hcommit := minHeap.NewCommitHeap()
	//
	//// start thread 开启多个线程，每个线程从Hready中抽取任务进行执行，执行完成后放入Hcommit中
	//for i := 0; i < threads; i++ {
	//	go func(threadID int) {
	//		thread := core.NewThread(threadID, stateDb, stateDbForConstruction, Hready, Hcommit, startBlock, blockContext, config.MainnetChainConfig)
	//		thread.Run(PrintDetails)
	//	}(i)
	//}
	//
	//// 构建依赖图，所有交易的执行都遵循初始的快照版本，也即storageVersion=-1的状态版本
	//fmt.Println("开始构建依赖图")
	//var dg *dependencyGraph.DependencyGraph
	//time1 := time.Now()
	//dg = dependencyGraph.ConstructDependencyGraph(len(concurrentTxs), Hready, Hcommit, stateDbForConstruction)
	////fmt.Printf("依赖图构建完成，如下所示，map[交易]=>该交易依赖的交易中序号最大的一个：\n%+v\n", dg.TxDependencyMap)
	//end1 := time.Since(time1)
	//fmt.Printf("依赖图构建完成, 耗时：%s\n", end1)
	//
	//// 执行DCC-DA算法
	//fmt.Println("开始执行DCC-DA")
	//time2 := time.Now()
	//DCCDA(len(concurrentTxs), Htxs, Hready, Hcommit, stateDb, dg)
	//end2 := time.Since(time2)
	//fmt.Printf("DCC-DA执行结束, 耗时：%s\n", end2)

	return nil
}

// TestICSEPerfect test re-execution overhead under perfect transaction dependencies
func TestICSEPerfect(threads int) error {
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
	//集合810~849区块中的所有交易，总共5577个
	txLen := 0
	min, max, addSpan := big.NewInt(14000001), big.NewInt(14000081), big.NewInt(1)
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
	// 取810号区块并将原810~849区块中所有交易放入，构造本项目所用的一个大区块startBlock
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

	// 新建所有交易共用的数据库stateDB，储存多版本stateobject
	stateDb, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps) // IcseStateDB类型，表征一个状态，需要由db变量构造而来
	if stateDb == nil {
		return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
	}
	// 再准备一个状态数据库，用于模拟执行交易从而生成依赖图
	stateDbForConstruction := stateDb.Copy()

	// 尚不可执行的交易队列（因为storage<next，所以尚不可执行）
	Htxs := minHeap.NewTxsHeap()
	// 已经可以执行，但是处于等待状态的交易队列
	Hready := minHeap.NewReadyHeap()
	// 执行完毕，等待验证的交易队列
	Hcommit := minHeap.NewCommitHeap()

	// 开启一个线程串行构建依赖图
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		thread := core.NewThread(0, stateDb, stateDbForConstruction, Hready, Hcommit, startBlock, blockContext, config.MainnetChainConfig)
		thread.SingleRun(len(concurrentTxs), PrintDetails)
	}()

	// 构建依赖图，所有交易的执行都遵循初始的快照版本，也即storageVersion=-1的状态版本
	fmt.Println("开始构建依赖图")
	var dg *dependencyGraph.DependencyGraph
	time1 := time.Now()
	dg = dependencyGraph.ConstructDependencyGraph(len(concurrentTxs), Hready, Hcommit, stateDbForConstruction)
	//fmt.Printf("依赖图构建完成，如下所示，map[交易]=>该交易依赖的交易中序号最大的一个：\n%+v\n", dg.TxDependencyMap)
	end1 := time.Since(time1)
	fmt.Printf("依赖图构建完成, 耗时：%s\n", end1)
	wg.Wait()

	// 执行DCC-DA算法
	for i := 0; i < threads; i++ {
		go func(threadID int) {
			thread := core.NewThread(threadID, stateDb, stateDbForConstruction, Hready, Hcommit, startBlock, blockContext, config.MainnetChainConfig)
			thread.Run(PrintDetails)
		}(i)
	}
	fmt.Println("开始执行DCC-DA")
	time2 := time.Now()
	DCCDA(len(concurrentTxs), Htxs, Hready, Hcommit, stateDb, dg)
	end2 := time.Since(time2)
	fmt.Printf("DCC-DA执行结束, 耗时：%s\n", end2)

	return nil
}

func DCCDA(txNum int, Htxs *minHeap.TxsHeap, Hready *minHeap.ReadyHeap, Hcommit *minHeap.CommitHeap, stateDb *state.IcseStateDB, dg *dependencyGraph.DependencyGraph) {
	// 按照论文的说法，如果没有给定交易依赖图，那么所有交易的sv(storageVersion)就默认设置为-1，否则设置为交易的依赖中序号最大的那个
	abortedMap := make(map[int]int)
	stateMap := make(map[int]int)
	storageMap := make(map[int]int)

	if dg == nil {
		for i := 0; i < txNum; i++ {
			Htxs.Push(-1, i, 0)
		}
	} else {
		for i := 0; i < txNum; i++ {
			Htxs.Push(dg.TxDependencyMap[i], i, 0)
		}
	}

	next := 0
	contractTxNum := 0
	transferTxNum := 0
	for next < txNum {
		// Stage 1: Schedule
		for true {
			txToCheckReady := Htxs.Pop()
			if txToCheckReady == nil {
				break
			}
			if txToCheckReady.StorageVersion > next-1 { //保证之前依赖的最大的交易已经被执行完
				Htxs.Push(txToCheckReady.StorageVersion, txToCheckReady.Index, txToCheckReady.Incarnation)
				break
			} else {
				Hready.Push(txToCheckReady.Index, txToCheckReady.Incarnation, txToCheckReady.StorageVersion, false)
			}
		}

		// Stage 2: Execution
		// thread will fetch tasks from Hready itself
		// after execution, tasks will be push into Hcommit

		// Stage 3: Commit/Abort
		for true {
			txToCommit := Hcommit.Pop()
			if txToCommit == nil {
				break
			}
			if txToCommit.Index != next {
				//abortedMap[txToCommit.Index]++
				Hcommit.Push(txToCommit)
				break
			}

			// 验证依赖版本sv+1到id-1所有交易有没有存在写的行为
			//aborted := false
			//for i := txToCommit.StorageVersion + 1; i < txToCommit.Index-1; i++ {
			//	if stateDb.ValidateReadSet(txToCommit.LastReads, i) {
			//		aborted = true
			//		break
			//	}
			//}
			aborted := stateDb.ValidateReadSet(txToCommit.LastReads, txToCommit.Index)
			if aborted {
				abortedMap[txToCommit.Index]++
				isContract := false
				for _, value := range txToCommit.LastWrites {
					if len(value.AccessedSlots) > 0 {
						isContract = true
					}
				}
				if isContract {
					storageMap[txToCommit.Index]++
				} else {
					stateMap[txToCommit.Index]++
				}

				//fmt.Printf("交易%+v验证失败\n", txToCommit.Index)
				if PrintDetails {
					fmt.Printf("交易%+v验证失败\n", txToCommit)
				}
				Htxs.Push(txToCommit.Index-1, txToCommit.Index, txToCommit.Incarnation+1)
			} else {
				if PrintDetails {
					fmt.Printf("交易%+v验证成功\n", txToCommit)
				}
				//Commit(txToCommit)
				isContract := false
				for _, value := range txToCommit.LastWrites {
					if len(value.AccessedSlots) > 0 {
						isContract = true
					}
				}
				if isContract {
					contractTxNum++
				} else {
					transferTxNum++
				}
				stateDb.Record(&state.TxInfoMini{Index: txToCommit.Index, Incarnation: txToCommit.Incarnation}, txToCommit.LastReads, txToCommit.LastWrites)
				next += 1
			}
		}
	}

	abortedNum := 0
	abortedTxNum := 0
	for _, num := range abortedMap {
		abortedNum += num
		abortedTxNum++
	}
	fmt.Printf("丢弃交易的占比: %.3f\n", float64(abortedTxNum)/float64(txNum))
	fmt.Printf("丢弃总次数: %d, 平均每个交易的丢弃次数: %.3f\n", abortedNum, float64(abortedNum)/float64(abortedTxNum))

	abortedContractNum := 0
	abortedContractTxNum := 0
	for _, num := range storageMap {
		abortedContractNum += num
		abortedContractTxNum++
	}
	fmt.Printf("丢弃合约交易的占比: %.3f\n", float64(abortedContractTxNum)/float64(contractTxNum))
	fmt.Printf("丢弃合约交易总次数: %d, 平均每个合约交易的丢弃次数: %.3f\n", abortedContractNum, float64(abortedContractNum)/float64(abortedContractTxNum))

	abortedTransferNum := 0
	abortedTransferTxNum := 0
	for _, num := range stateMap {
		abortedTransferNum += num
		abortedTransferTxNum++
	}
	fmt.Printf("丢弃转账交易的占比: %.3f\n", float64(abortedTransferTxNum)/float64(transferTxNum))
	fmt.Printf("丢弃转账交易总次数: %d, 平均每个转账交易的丢弃次数: %.3f\n", abortedTransferNum, float64(abortedTransferNum)/float64(abortedTransferTxNum))
}

func Commit(tx *minHeap.CommitItem) {
	// 提交阶段什么都不做，因为每笔交易执行完都会已经将写集放入公用的statedb中的多版本stateobject中，最终结果就参照该公用数据库
	// 每笔交易每次执行时都参照一个稳定的快照版本sv，这是通过读取多版本stateobject时无法读到比sv更大版本的数据实现的
}
