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
	minHeap "icse/min_heap"
	"math/big"
)

func main() {
	err := TestICSE(6)
	if err != nil {
		panic(err)
	}
}

func TestICSE(threads int) error {
	// 数据库封装程度由低到高，ethdb.Database是接口类型，rawdb是一个leveldb或pebble加上freezer
	// ethdb.Database是geth的数据库抽象，rawdb包中给出了一个使用leveldb构造的一个接口具体实现实现，是disk db
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
	//集合810~849区块中的所有交易
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
	// 取810号区块并将原810~849区块中所有交易放入，构造本项目所用的区块
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

	//// stateDB存在于内存中，stateDB中的db字段为stateCache
	//startStateDB, err := state.New(parent.Root, stateCache, nil)
	//if err != nil {
	//	fmt.Println(err)
	//}
	// 新建所有交易共用的数据库stateDB
	stateDb, err := state.NewIcseStateDB(*parentRoot, stateCache, snaps) // IcseStateDB类型，表征一个状态，需要由db变量构造而来
	if stateDb == nil {
		return fmt.Errorf("function state.NewIcseStateDB error: %s", err)
	}

	// 开始
	Htxs := minHeap.NewTxsHeap()
	for i, _ := range concurrentTxs {
		Htxs.Push(-1, i, 0)
	}
	Hready := minHeap.NewReadyHeap()
	Hcommit := minHeap.NewCommitHeap()
	next := 0

	// start thread
	for i := 0; i < threads; i++ {
		go func() {
			thread := core.NewThread(stateDb, startBlock, blockContext, Hready, Hcommit)
			thread.Run()
		}()
	}

	// start scheduler:  OCC-DA
	for next < len(concurrentTxs) {
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
				Hready.Push(txToCheckReady.Index, txToCheckReady.Incarnation, txToCheckReady.StorageVersion)
			}
		}

		// Stage 2: Execution
		// thread will fetch tasks itself
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
			aborted := stateDb.ValidateReadSet(txToCommit.Index)
			if aborted {
				Htxs.Push(txToCommit.Index-1, txToCommit.Index, txToCommit.Incarnation+1)
			} else {
				Commit(txToCommit)
				next += 1
			}
		}
	}

	return nil
}

func Commit(tx *minHeap.CommitItem) {
	// 提交阶段什么都不用做，因为每笔交易执行完都会已经将自己的写集放入公用的statedb中的多版本stateobject中
	// 每笔交易每次执行时都参照一个稳定的快照版本sv，这是通过读取多版本stateobject时无法读到比sv更大的数据实现的
}
