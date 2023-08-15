package main

import (
	"fmt"
	"icse/config"
	"icse/core"
	"icse/core/state"
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
	// 取810号区块并将所有交易放入
	startBlock, err := database.GetBlockByNumber(db, new(big.Int).SetUint64(9776810))
	if err != nil {
		return fmt.Errorf("get block 9776810 error: %s", err)
	}
	startBlock.AddTransactions(concurrentTxs) // 构造出一个区块拥有810~850区块中的所有交易

	var (
		parent *types.Header = blockPre.Header()
		// 该state.Database接口的具体类型为state.cachingDB，其中的disk字段为db
		stateCache state.Database = database.NewStateCache(db)
		// EVM执行所需要的区块上下文，一次性生成且后续不能改动
		blockContext = core.NewEVMBlockContext(startBlock.Header(), db, nil)
	)

	// stateDB存在于内存中，stateDB中的db字段为stateCache
	startStateDB, err := state.New(parent.Root, stateCache, nil)
	if err != nil {
		fmt.Println(err)
	}

	Htxs := minHeap.NewTxsHeap()
	for i, _ := range concurrentTxs {
		Htxs.Push(-1, i)
	}
	Hready := minHeap.NewReadyHeap()
	Hcommit := minHeap.NewCommitHeap()
	next := 0

	// start thread
	for i := 0; i < threads; i++ {
		go func() {
			thread := core.NewThread(startStateDB, startBlock, blockContext, Hready)
			thread.Run()
		}()
	}

	// start scheduler
	for next < len(concurrentTxs) {
		// Stage 1: Schedule
		for true {
			txToCheckReady := Htxs.Pop()
			if txToCheckReady == nil {
				break
			}
			if txToCheckReady.StorageVersion > next-1 {
				Htxs.Push(txToCheckReady.StorageVersion, txToCheckReady.Index)
				break
			} else {
				Hready.Push(txToCheckReady.Index, txToCheckReady.StorageVersion)
			}
		}

		// Stage 2: Execution
		// thread will fetch its tasks iself

		// Stage 3: Commit/Abort
		for true {
			txToCommit := Hcommit.Pop()
			if txToCommit == nil {
				break
			}
			if txToCommit.Index != next {
				Hcommit.Push(txToCommit.Index, txToCommit.StorageVersion)
				break
			}
			aborted := Validate(txToCommit)
			if aborted {
				Htxs.Push(txToCommit.Index-1, txToCommit.Index)
			} else {
				Commit(txToCommit)
				next += 1
			}
		}
	}

	return nil
}
