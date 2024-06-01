package database

import "runtime"

type RawConfig struct {
	Path      string
	Cache     int
	Handles   int
	Ancient   string
	Namespace string
	ReadOnly  bool
}

func DefaultRawConfig() *RawConfig {
	if runtime.GOOS == "darwin" { // MacOS
		return &RawConfig{
			//Path:      "/Volumes/ETH_DATA/ethereum/geth/chaindata",
			//Path:      "/Volumes/ETH_DATA/ethereumdata/copychain",
			//Path: "/Users/zsj/Downloads/ethereumdata/copychain",
			Path: "/Users/zsj/Desktop/BCTS小组任务/智能合约应用逻辑课题/ethereumdata/copychain",
			//Path: "/home/ubuntu/ethereumdata/copychain",
			//Path:    "/Users/zsj/Desktop/BCTS小组任务/智能合约应用逻辑课题/prophetEVM/copychain",
			Cache:   2048,
			Handles: 5120,
			//Ancient:   "/Volumes/ETH_DATA/ethereumdata/copychain/ancient",
			//Ancient: "/Users/zsj/Downloads/ethereumdata/copychain/ancient",
			Ancient: "/Users/zsj/Desktop/BCTS小组任务/智能合约应用逻辑课题/ethereumdata/copychain/ancient",
			//Ancient: "/home/ubuntu/ethereumdata/copychain/ancient",
			//Ancient:   "/Users/zsj/Desktop/BCTS小组任务/智能合约应用逻辑课题/prophetEVM/copychain/ancient",
			Namespace: "eth/db/chaindata/",
			ReadOnly:  false,
		}
	} else {
		return &RawConfig{
			//Path:      "/experiment/ethereum/geth/chaindata",
			Path:    "/home/ubuntu/ethereumdata/copychain",
			Cache:   2048,
			Handles: 5120,
			//Ancient:   "/experiment/ethereum/geth/chaindata/ancient",
			Ancient:   "/home/ubuntu/ethereumdata/copychain/ancient",
			Namespace: "eth/db/chaindata/",
			ReadOnly:  false,
		}
	}
}

type StateDBConfig struct {
	Cache     int
	Journal   string
	Preimages bool
}

func defaultStateDBConfig() *StateDBConfig {
	if runtime.GOOS == "darwin" { // MacOS
		return &StateDBConfig{
			Cache: 614,
			//Journal:   "/Users/darcywep/Projects/ethereum/execution/geth/triecache",
			Journal:   "/Volumes/ETH_DATA/ethereum/geth/triecache",
			Preimages: false,
		}
	} else {
		return &StateDBConfig{
			Cache:     614,
			Journal:   "/experiment/ethereum/geth/triecache",
			Preimages: false,
		}
	}
}
