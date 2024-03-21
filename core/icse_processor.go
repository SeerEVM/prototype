package core

import (
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
	"math/big"
	"prophetEVM/core/state"
	"prophetEVM/core/types"
	"prophetEVM/core/vm"
	"sort"
)

func applyIcseTransaction(msg *Message, gp *GasPool, statedb *state.IcseTransaction, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, evm *vm.EVM) (*types.Receipt, error) {
	// Create a new context to be used in the EVM environment.
	txContext := NewEVMTxContext(msg, tx.Hash(), &big.Int{})
	evm.Reset(txContext, statedb)

	// Apply the transaction to the current state (included in the env).
	// 真正执行并更新stmTxStateDB的stateObjects，在官方的库中，后续调用Finalise才会将这些改动应用到statedb中
	result, err := ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
	}

	// Update the state with pending changes.
	var root []byte
	//if config.IsByzantium(blockNumber) {
	//	stmStateDB.Finalise(true, statedb.Index)
	//} else {
	//	root = stmStateDB.IntermediateRoot(config.IsEIP158(blockNumber), statedb.Index).Bytes()
	//}
	*usedGas += result.UsedGas

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas}
	if result.Failed() {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas

	// If the transaction created a contract, store the creation address in the receipt.
	if msg.To == nil {
		receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
	}

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockNumber.Uint64(), blockHash)
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	return receipt, err
}

// applyProphetTransaction pre-executes a transaction to cache fast path info and conduct pre-execution repair if necessary
func applyProphetTransaction(msg *Message, gp *GasPool, evm *vm.EVM, txSet map[common.Hash]*types.Transaction, baseFee *big.Int) error {
	if _, err := ApplyMessage(evm, msg, gp); err != nil {
		return err
	}

	if evm.Interpreter().GetRepair() {
		var repairedTXs []common.Hash
		repairedLoc := evm.Interpreter().GetRepairedLoc()
		for addr, slotMap := range repairedLoc {
			for slot, offsetMap := range slotMap {
				for str := range offsetMap {
					var slotInt, offset uint256.Int
					slotInt.SetBytes(slot.Bytes())
					offset.SetBytes([]byte(str))
					txs, err2 := evm.MVCache.GetRepairTXs(addr, slotInt, offset)
					if err2 != nil {
						return err2
					}
					repairedTXs = append(repairedTXs, txs...)
				}
			}
		}
		txs := sortTXs(repairedTXs, txSet, baseFee)
		for _, t := range txs {
			ret, _ := evm.PreExecutionTable.GetResult(t.Hash())
			txContext := ret.GetTxContext()
			vmenv := vm.NewEVM2(evm.Context, txContext, evm.StateDB, evm.VarTable, evm.PreExecutionTable,
				evm.MVCache, evm.ChainConfig(), vm.Config{EnablePreimageRecording: false}, true)
			vmenv.Interpreter().SetFastEnabled()
			if err3 := repair(txContext, vmenv, t, ret); err3 != nil {
				return err3
			}
		}
	}
	return nil
}

// applyFastPath executes a transaction in a fast-path mode
func applyFastPath(msg *Message, tx *types.Transaction, evm *vm.EVM, result *vm.Result, gp *GasPool, statedb *state.IcseTransaction, blockNumber *big.Int, blockHash common.Hash, usedGas *uint64) (*types.Receipt, error) {
	var (
		res     *ExecutionResult
		err     error
		isBreak bool
	)

	// directly executes contract creation tx
	if tx.To() == nil {
		res, err = ApplyMessage(evm, msg, gp)
		if err != nil {
			return nil, err
		}
	} else {
		brs := result.GetBranches()
		for _, br := range brs {
			// update the sstore info
			sstores := br.GetSstoreInfo()
			for _, sstore := range sstores {
				if err = updateSstore(sstore, evm, false); err != nil {
					return nil, err
				}
			}

			isTaken, err := checkBranch(evm, tx, br, false)
			if err != nil {
				return nil, err
			}
			if isTaken != br.GetBranchDirection() {
				// encounter inconsistent path, conduct fast-path repair
				var callMap map[int]*vm.Snapshot
				var stackElement uint256.Int
				curSnapshots := br.GetSnapshots()
				callStack := result.GetCallStack()
				latestSnapshot := curSnapshots[len(curSnapshots)-1]
				if len(callStack) > 0 {
					// internal call exists
					for depth, sps := range callStack {
						// put the latest snapshot under each depth into the call map
						if depth == latestSnapshot.GetDepth() {
							callMap[depth] = latestSnapshot
							continue
						}
						callMap[depth] = sps[len(sps)-1]
					}
				} else {
					callMap[1] = latestSnapshot
				}
				evm.Interpreter().SetCallMap(callMap)

				// modify the branch info
				if isTaken {
					pc := latestSnapshot.GetPC()
					latestSnapshot.UpdatePC(pc + 1)
					br.DecideDirection(1)
					if br.GetJudgement() != "EQ" {
						stackElement.SetUint64(1)
						latestSnapshot.GetStack().UpdatePeek(stackElement)
					}
				} else {
					br.DecideDirection(0)
					if br.GetJudgement() == "EQ" {
						jumpPc := latestSnapshot.GetJumpPC()
						latestSnapshot.UpdatePC(jumpPc)
					} else {
						pc := latestSnapshot.GetPC()
						latestSnapshot.UpdatePC(pc + 1)
						stackElement.SetUint64(0)
						latestSnapshot.GetStack().UpdatePeek(stackElement)
					}
				}

				// TODO: 更新分支历史与感知器，这里可能需要加锁
				if err = evm.VarTable.AddHistory(br); err != nil {
					return nil, err
				}

				// recover execution from the initial snapshot
				res, err = FastApplyMessage(evm, msg, gp, callMap[1].GetContract().Gas, nil)
				if err != nil {
					return nil, err
				}
				isBreak = true
				break
			}
		}
		// all the branches are satisfied, execute the snapshot
		if !isBreak {
			finalSnapshot := result.GetFinalSnapshot()
			for _, sstore := range finalSnapshot.GetSstoreInfo() {
				if err = updateSstore(sstore, evm, false); err != nil {
					return nil, err
				}
			}
			res, err = FastApplyMessage(evm, msg, gp, 0, finalSnapshot)
			if err != nil {
				return nil, err
			}
		}
	}

	// Update the state with pending changes.
	var root []byte
	*usedGas += res.UsedGas

	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: *usedGas}
	if res.Failed() {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = res.UsedGas

	// If the transaction created a contract, store the creation address in the receipt.
	if msg.To == nil {
		receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
	}

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockNumber.Uint64(), blockHash)
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	return receipt, nil
}

// repair conducts pre-execution repair
func repair(context vm.TxContext, evm *vm.EVM, tx *types.Transaction, result *vm.Result) error {
	brs := result.GetBranches()
	for i, br := range brs {
		// update the sstore info
		sstores := br.GetSstoreInfo()
		for _, sstore := range sstores {
			if err := updateSstore(sstore, evm, true); err != nil {
				return err
			}
		}

		isTaken, err := checkBranch(evm, tx, br, true)
		if err != nil {
			return err
		}
		if isTaken != br.GetBranchDirection() {
			// encounter inconsistent path, conduct fast-path repair
			var callMap map[int]*vm.Snapshot
			var stackElement uint256.Int
			curSnapshots := br.GetSnapshots()
			callStack := result.GetCallStack()
			latestSnapshot := curSnapshots[len(curSnapshots)-1]
			if len(callStack) > 0 {
				// internal call exists
				for depth, sps := range callStack {
					// put the latest snapshot under each depth into the call map
					if depth == latestSnapshot.GetDepth() {
						callMap[depth] = latestSnapshot
						continue
					}
					callMap[depth] = sps[len(sps)-1]
				}
			} else {
				callMap[1] = latestSnapshot
			}
			evm.Interpreter().SetCallMap(callMap)

			// modify the branch info
			if isTaken {
				pc := latestSnapshot.GetPC()
				latestSnapshot.UpdatePC(pc + 1)
				br.DecideDirection(1)
				if br.GetJudgement() != "EQ" {
					stackElement.SetUint64(1)
					latestSnapshot.GetStack().UpdatePeek(stackElement)
				}
			} else {
				br.DecideDirection(0)
				if br.GetJudgement() == "EQ" {
					jumpPc := latestSnapshot.GetJumpPC()
					latestSnapshot.UpdatePC(jumpPc)
				} else {
					pc := latestSnapshot.GetPC()
					latestSnapshot.UpdatePC(pc + 1)
					stackElement.SetUint64(0)
					latestSnapshot.GetStack().UpdatePeek(stackElement)
				}
			}
			result.Reset(i, latestSnapshot.GetDepth(), latestSnapshot.GetPC())

			// recover execution from the initial snapshot
			_, _, err2 := evm.Call(vm.AccountRef(context.Origin), *context.To, context.Data, callMap[1].GetContract().Gas, context.Value)
			if err2 != nil {
				return err2
			}
			break
		}
	}
	return nil
}

// sort transactions in a set according to their gas fees
func sortTXs(repairedTXs []common.Hash, txSet map[common.Hash]*types.Transaction, baseFee *big.Int) []*types.Transaction {
	var (
		identicalTXMap = make(map[common.Hash]struct{})
		output         []*types.Transaction
		sortedFees     []int
		feeMap         = make(map[int][]*types.Transaction)
	)

	for _, txID := range repairedTXs {
		if _, exist := identicalTXMap[txID]; !exist {
			tx := txSet[txID]
			tip := int(math.BigMin(tx.GasTipCap(), new(big.Int).Sub(tx.GasFeeCap(), baseFee)).Int64())
			sortedFees = append(sortedFees, tip)
			feeMap[tip] = append(feeMap[tip], tx)
			identicalTXMap[txID] = struct{}{}
		}
	}

	sort.Ints(sortedFees)
	for _, fee := range sortedFees {
		txs := feeMap[fee]
		output = append(output, txs...)
	}
	return output
}

// checkBranch checks whether the current state satisfies the stored branch info to perform quick path
func checkBranch(evm *vm.EVM, tx *types.Transaction, branch *vm.BranchContext, isMultiVersion bool) (bool, error) {
	var (
		firstVal, secondVal uint256.Int
		compact, compact2   bool
		err                 error
	)
	su := branch.GetStateUnit()
	slot := su.GetSlot()
	offset := su.GetOffset()
	bits := su.GetBits()
	if offset.Uint64() > 0 && bits < 256 {
		compact = true
	}

	if isMultiVersion {
		// utilizes the multi-version cache to check
		firstVal, err = vm.GetComparedVal(evm, tx.Hash(), branch.GetAddr(), slot, offset, bits, su, compact, true)
	} else {
		// utilizes the stateDB to check
		firstVal, err = vm.GetComparedVal(evm, tx.Hash(), branch.GetAddr(), slot, offset, bits, su, compact, false)
	}
	if err != nil {
		return false, err
	}

	tracingUnit := branch.GetTracingUnit()
	if branch.IsVar() {
		cUnit, _ := tracingUnit.(*vm.StateUnit)
		slot2 := cUnit.GetSlot()
		offset2 := cUnit.GetOffset()
		bits2 := cUnit.GetBits()
		if offset2.Uint64() > 0 && bits2 < 256 {
			compact2 = true
		}

		if isMultiVersion {
			// utilizes the multi-version cache to check
			secondVal, err = vm.GetComparedVal(evm, tx.Hash(), branch.GetAddr(), slot2, offset2, bits2, cUnit, compact2, true)
		} else {
			// utilizes the stateDB to check
			secondVal, err = vm.GetComparedVal(evm, tx.Hash(), branch.GetAddr(), slot2, offset2, bits2, cUnit, compact2, false)
		}
		if err != nil {
			return false, err
		}
	} else {
		secondVal = tracingUnit.GetValue()
	}

	// compute the current branch direction
	direction := branch.GetJudgementDirection()
	judgement := vm.StringToOp(branch.GetJudgement())
	if direction {
		vm.Compute(&firstVal, &secondVal, judgement)
		if secondVal.Uint64() == 1 {
			return true, nil
		}
	} else {
		vm.Compute(&secondVal, &firstVal, judgement)
		if firstVal.Uint64() == 1 {
			return true, nil
		}
	}
	return false, nil
}

// updateSstore re-computes the stored value according to the cached sstore info
func updateSstore(sstore *vm.SstoreInfo, evm *vm.EVM, isMultiVersion bool) error {
	contractAddr := sstore.GetCallerAddr()
	locUnit := sstore.GetLocUnit()
	valUnit := sstore.GetValUnit()
	loc := locUnit.GetValue()
	originalVal := sstore.GetUpdatedValue()
	if sunit, ok := valUnit.(*vm.StateUnit); ok {
		slot := sunit.GetSlot()
		if sstore.GetCompact() {
			newVal, err := computeCompactedVar(evm, evm.TxContext.ID, contractAddr, originalVal, sunit, isMultiVersion)
			if err != nil {
				return err
			}
			if isMultiVersion {
				if newVal.Cmp(&originalVal) != 0 {
					// update the value stored in MVCache and the sstore info
					if _, err2 := evm.MVCache.SetStorageForWrite(contractAddr, loc, newVal, evm.TxContext.ID, evm.TxContext.GasTip); err2 != nil {
						return err2
					}
					sstore.UpdateVal(newVal)
				}
			} else {
				compactedSstore(evm, contractAddr, loc, newVal, sunit)
			}
		} else {
			zeroOffset := uint256.Int{}
			zeroOffset.SetUint64(0)
			newVal, err := vm.GetComparedVal(evm, evm.TxContext.ID, contractAddr, slot, zeroOffset, 256, sunit, false, isMultiVersion)
			if err != nil {
				return err
			}
			if isMultiVersion {
				if newVal.Cmp(&originalVal) != 0 {
					// update the value stored in MVCache and the sstore info
					if _, err2 := evm.MVCache.SetStorageForWrite(contractAddr, loc, newVal, evm.TxContext.ID, evm.TxContext.GasTip); err2 != nil {
						return err2
					}
					sstore.UpdateVal(newVal)
				}
			} else {
				evm.StateDB.SetState(contractAddr, loc.Bytes32(), newVal.Bytes32())
			}
		}
	} else {
		// 直接赋值，在真正执行时，直接存储到stateDB
		if !isMultiVersion {
			evm.StateDB.SetState(contractAddr, loc.Bytes32(), originalVal.Bytes32())
		}
	}
	return nil
}

// computeCompactedVar computes the latest stored value of a state variable when storage is compacted
func computeCompactedVar(evm *vm.EVM, txID common.Hash, contractAddr common.Address, originalVal uint256.Int, su *vm.StateUnit, isMultiVersion bool) (uint256.Int, error) {
	tracers := su.GetTracer()
	lastTr := tracers[len(tracers)-1]
	op := vm.StringToOp(lastTr.GetOps())
	if op == vm.OR {
		unit := lastTr.GetAttaching()
		if sunit, ok := unit.(*vm.StateUnit); ok {
			var compact bool
			sunit.DeleteLastOp()
			slot := sunit.GetSlot()
			offset := sunit.GetOffset()
			bits := sunit.GetBits()
			if offset.Uint64() > 0 && bits < 256 {
				compact = true
			}
			newVal, err := vm.GetComparedVal(evm, txID, contractAddr, slot, offset, bits, sunit, compact, isMultiVersion)
			if err != nil {
				return uint256.Int{}, err
			}
			return newVal, nil
		} else {
			return originalVal, nil
		}
	}
	return uint256.Int{}, errors.New("incorrect operation record")
}

// compactedSstore performs compacted sstore operation based on the latest value of a state variable
func compactedSstore(evm *vm.EVM, contractAddr common.Address, loc, newVal uint256.Int, sunit *vm.StateUnit) {
	var res1, res2 *uint256.Int
	val := evm.StateDB.GetState(contractAddr, loc.Bytes32())
	valu := uint256.Int{}
	valu.SetBytes(val.Bytes())
	offset := sunit.GetOffset()
	bits := sunit.GetBits()
	mask := vm.MakeMask(bits)
	// 计算第一部分
	res1.Mul(mask, &offset)
	res1.Not(res1)
	res1.And(res1, &valu)
	// 计算第二部分
	if sunit.GetSignExtend() {
		opVal := uint256.Int{}
		opVal.SetUint64(uint64(bits/8 - 1))
		newVal.ExtendSign(&newVal, &opVal)
	}
	res2.And(mask, &newVal)
	res2.Mul(res2, &offset)
	// 两部分Or操作
	res2.Or(res2, res1)
	evm.StateDB.SetState(contractAddr, loc.Bytes32(), res2.Bytes32())
}
