package main

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"icse/config"
	"icse/core"
	"icse/core/state"
	"icse/core/state/snapshot"
	"icse/core/types"
	"icse/database"
	"icse/dependencyGraph"
	"icse/minHeap"
	"math/big"
)

// PrintDetails 是否打印详细信息
const PrintDetails = false

func main() {
	err := TestICSE(4)
	if err != nil {
		panic(err)
	}
}

func TestICSE(threads int) error {
	// 数据库封装程度由低到高，ethdb.Database是接口类型，rawdb是一个leveldb或pebble加上freezer
	// ethdb.Database是geth的数据库抽象，rawdb包中给出了一个使用leveldb构造的一个接口具体实现实现，属于disk db
	rawConfig := database.DefaultRawConfig()
	rawConfig.Path = "./copychain"
	rawConfig.Ancient = "./copychain/ancient"
	db, err := database.OpenDatabaseWithFreezer(&config.DefaultsEthConfig, rawConfig)
	if err != nil {
		return fmt.Errorf("open leveldb error: %s", err)
	}
	defer db.Close()

	// db中有9976 809~9976 859号区块，测试9776 809号能否打开
	blockPre, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(9776809))
	if err != nil {
		return fmt.Errorf("function GetBlockByNumber error: %s", err)
	}
	//集合810~849区块中的所有交易，总共5577个
	min, max, addSpan := big.NewInt(9776810), big.NewInt(9776850), big.NewInt(1)
	var concurrentTxs types.Transactions
	for i := min; i.Cmp(max) == -1; i = i.Add(i, addSpan) {
		block, err := database.GetBlockByNumber(db, i)
		if err != nil {
			return fmt.Errorf("get block %s error: %s", i.String(), err)
		}
		newTxs := block.Transactions()
		concurrentTxs = append(concurrentTxs, newTxs...)
	}
	// 取810号区块并将原810~849区块中所有交易放入，构造本项目所用的一个大区块startBlock
	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(9776810))
	if err != nil {
		return fmt.Errorf("get block 9776810 error: %s", err)
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
	stateDbForSimulation := stateDb.Copy()

	// 尚不可执行的交易队列（因为storage<next，所以尚不可执行）
	Htxs := minHeap.NewTxsHeap()
	// 已经可以执行，但是处于等待状态的交易队列
	Hready := minHeap.NewReadyHeap()
	// 执行完毕，等待验证的交易队列
	Hcommit := minHeap.NewCommitHeap()

	// start thread 开启多个线程，每个线程从Hready中抽取任务进行执行，执行完成后放入Hcommit中
	for i := 0; i < threads; i++ {
		go func(threadID int) {
			thread := core.NewThread(threadID, stateDb, stateDbForSimulation, Hready, Hcommit, startBlock, blockContext, config.MainnetChainConfig)
			thread.Run(PrintDetails)
		}(i)
	}

	// 构建依赖图，所有交易的执行都遵循初始的快照版本，也即storageVersion=-1的状态版本
	fmt.Println("开始构建依赖图")
	var dg *dependencyGraph.DependencyGraph
	dg = dependencyGraph.ConstructDependencyGraph(len(concurrentTxs), Hready, Hcommit, stateDbForSimulation)
	fmt.Printf("依赖图构建完成，如下所示，map[交易]=>该交易依赖的交易中序号最大的一个：\n%+v\n", dg.TxDependencyMap)

	// 执行DCC-DA算法
	fmt.Println("开始执行DCC-DA")
	DCCDA(len(concurrentTxs), Htxs, Hready, Hcommit, stateDb, dg)
	fmt.Println("DCC-DA执行结束")

	return nil
}

func DCCDA(txNum int, Htxs *minHeap.TxsHeap, Hready *minHeap.ReadyHeap, Hcommit *minHeap.CommitHeap, stateDb *state.IcseStateDB, dg *dependencyGraph.DependencyGraph) {
	// 按照论文的说法，如果没有给定交易依赖图，那么所有交易的sv(storageVersion)就默认设置为-1，否则设置为交易的依赖中序号最大的那个
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
	for next < txNum {
		// Stage 1: Schedule
		for true {
			txToCheckReady := Htxs.Pop()
			if txToCheckReady == nil {
				break
			}
			if txToCheckReady.StorageVersion > next-1 {
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
				Hcommit.Push(txToCommit.Index, txToCommit.Incarnation, txToCommit.StorageVersion)
				break
			}
			aborted := !stateDb.ValidateReadSet(txToCommit.Index, txToCommit.Incarnation, PrintDetails)
			if aborted {
				if PrintDetails {
					fmt.Printf("交易%+v验证失败\n", txToCommit)
				}
				Htxs.Push(txToCommit.Index-1, txToCommit.Index, txToCommit.Incarnation+1)
			} else {
				if PrintDetails {
					fmt.Printf("交易%+v验证成功\n", txToCommit)
				}
				Commit(txToCommit)
				next += 1
			}
		}
	}
}

func Commit(tx *minHeap.CommitItem) {
	// 提交阶段什么都不做，因为每笔交易执行完都会已经将写集放入公用的statedb中的多版本stateobject中，最终结果就参照该公用数据库
	// 每笔交易每次执行时都参照一个稳定的快照版本sv，这是通过读取多版本stateobject时无法读到比sv更大版本的数据实现的
}
